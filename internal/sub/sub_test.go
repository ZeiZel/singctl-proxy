package sub

import (
	"encoding/base64"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A handful of realistic share links reused across the table-driven tests
// below. link2 deliberately carries a comma INSIDE its alpn param — that is
// the case Parse must not let a naive comma-split corrupt.
const (
	link1 = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443?security=none#server-one"
	link2 = "trojan://s3cr3t@5.6.7.8:8443?security=tls&sni=example.com&alpn=h2,http/1.1#server-two"
	link3 = "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@9.9.9.9:8888#server-three"
)

func TestParse_OK(t *testing.T) {
	plainList := link1 + "\n" + link2 + "\n" + link3

	tests := []struct {
		name string
		body []byte
		want []string
	}{
		{
			name: "plain text, newline separated",
			body: []byte(plainList),
			want: []string{link1, link2, link3},
		},
		{
			name: "comma separated, including a link whose OWN alpn contains a comma",
			// The separator here is a comma, and link2's alpn=h2,http/1.1 also
			// contains one. splitLinks must find link2's next-scheme boundary
			// (ss://) rather than cutting at the alpn comma.
			body: []byte(link2 + "," + link3),
			want: []string{link2, link3},
		},
		{
			name: "semicolon separated",
			body: []byte(link1 + ";" + link3),
			want: []string{link1, link3},
		},
		{
			name: "CRLF line endings",
			body: []byte(link1 + "\r\n" + link2 + "\r\n" + link3),
			want: []string{link1, link2, link3},
		},
		{
			name: "leading and trailing whitespace around the whole body",
			body: []byte("   \n\t" + link1 + "  \n\t  "),
			want: []string{link1},
		},
		{
			name: "base64 standard, padded",
			body: []byte(base64.StdEncoding.EncodeToString([]byte(plainList))),
			want: []string{link1, link2, link3},
		},
		{
			name: "base64 standard, raw (no padding)",
			body: []byte(base64.RawStdEncoding.EncodeToString([]byte(plainList))),
			want: []string{link1, link2, link3},
		},
		{
			name: "base64 URL-safe, padded",
			body: []byte(base64.URLEncoding.EncodeToString([]byte(plainList))),
			want: []string{link1, link2, link3},
		},
		{
			name: "base64 URL-safe, raw (no padding)",
			body: []byte(base64.RawURLEncoding.EncodeToString([]byte(plainList))),
			want: []string{link1, link2, link3},
		},
		{
			name: "base64 body wrapped across multiple lines (common panel formatting)",
			body: []byte(wrapLines(base64.StdEncoding.EncodeToString([]byte(plainList)), 20)),
			want: []string{link1, link2, link3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.body)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("links mismatch\n got: %#v\nwant: %#v", got, tt.want)
			}
		})
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"empty body", []byte("")},
		{"body of blank lines only", []byte("\n\n   \n\t\n\n")},
		{"blank lines surrounding non-link text", []byte("\n\n\nhello, this is not a subscription\n\n\n")},
		{
			name: "HTML error page must be rejected, not silently produce zero links",
			body: []byte(`<html><head><title>404</title></head><body><h1>Not Found</h1><p>The requested subscription token is invalid.</p></body></html>`),
		},
		{"garbage that happens to be valid base64 but decodes to nonsense", []byte(base64.StdEncoding.EncodeToString([]byte("just some unrelated text with no links")))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.body)
			if err == nil {
				t.Fatalf("expected an error, got links: %#v", got)
			}
		})
	}
}

func TestParseMeta(t *testing.T) {
	tests := []struct {
		name string
		hdr  http.Header
		want Meta
	}{
		{
			name: "full subscription-userinfo plus interval and title",
			hdr: http.Header{
				"Subscription-Userinfo":   {"upload=1000; download=2000; total=5000000000; expire=1735689600"},
				"Profile-Update-Interval": {"24"},
				"Profile-Title":           {"My Panel"},
			},
			want: Meta{
				Title:          "My Panel",
				UpdateInterval: 24 * time.Hour,
				Upload:         1000,
				Download:       2000,
				Total:          5000000000,
				Expire:         time.Unix(1735689600, 0),
			},
		},
		{
			name: "partial userinfo — only download set",
			hdr: http.Header{
				"Subscription-Userinfo": {"download=500"},
			},
			want: Meta{Download: 500},
		},
		{
			name: "garbage values are ignored, not zero-valued panics",
			hdr: http.Header{
				"Subscription-Userinfo":   {"upload=abc; total=xyz; expire=notanumber"},
				"Profile-Update-Interval": {"not-a-number"},
			},
			want: Meta{},
		},
		{
			name: "negative interval is ignored (hours must be > 0)",
			hdr: http.Header{
				"Profile-Update-Interval": {"-5"},
			},
			want: Meta{},
		},
		{
			name: "profile-update-interval alone",
			hdr: http.Header{
				"Profile-Update-Interval": {"12"},
			},
			want: Meta{UpdateInterval: 12 * time.Hour},
		},
		{
			name: "expire=0 means unset, not the unix epoch",
			hdr: http.Header{
				"Subscription-Userinfo": {"expire=0"},
			},
			want: Meta{},
		},
		{
			name: "no headers at all",
			hdr:  http.Header{},
			want: Meta{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMeta(tt.hdr)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("meta mismatch\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

func TestParseMeta_Base64Title(t *testing.T) {
	title := "Мой Панель — VIP"
	encoded := "base64:" + base64.StdEncoding.EncodeToString([]byte(title))

	hdr := http.Header{"Profile-Title": {encoded}}
	got := ParseMeta(hdr)
	if got.Title != title {
		t.Errorf("Title = %q, want %q", got.Title, title)
	}
}

func TestParseMeta_Base64Title_RawEncoding(t *testing.T) {
	title := "No Padding Needed?"
	encoded := "base64:" + base64.RawStdEncoding.EncodeToString([]byte(title))

	hdr := http.Header{"Profile-Title": {encoded}}
	got := ParseMeta(hdr)
	if got.Title != title {
		t.Errorf("Title = %q, want %q", got.Title, title)
	}
}

func TestParseMeta_MalformedBase64TitleFallsBackToRaw(t *testing.T) {
	raw := "base64:not-valid-base64!!!"
	hdr := http.Header{"Profile-Title": {raw}}
	got := ParseMeta(hdr)
	if got.Title != raw {
		t.Errorf("Title = %q, want raw fallback %q", got.Title, raw)
	}
}

// wrapLines inserts a newline every n characters, simulating panels that
// wrap their base64 subscription body across multiple lines.
func wrapLines(s string, n int) string {
	var b strings.Builder
	for i := 0; i < len(s); i += n {
		end := i + n
		if end > len(s) {
			end = len(s)
		}
		b.WriteString(s[i:end])
		b.WriteByte('\n')
	}
	return b.String()
}

// sanity check that the constants above are actually recognised as links by
// the package's own scheme list, so a typo in the fixtures fails loudly
// instead of silently weakening every table test that uses them.
func TestFixtureLinksAreRecognised(t *testing.T) {
	for _, l := range []string{link1, link2, link3} {
		if !linkStart.MatchString(l) {
			t.Fatalf("fixture %q not recognised as a share link by linkStart", l)
		}
	}
}
