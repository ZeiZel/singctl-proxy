// Package sub — this file is the format dispatcher for Parse: a panel can
// serve share links wrapped in several shapes besides the base64/plain-text
// list sub.go itself handles (Remnawave, Marzban, Marzneshin, Hiddify and
// 3x-ui all support at least one of these, chosen by client User-Agent or an
// explicit query/path):
//
//   - a full sing-box JSON config, servers living in its outbounds array
//     (singbox_adapter.go);
//   - a Clash/Mihomo YAML config, servers living in its proxies: list
//     (clash_adapter.go);
//   - a bare JSON array of share links, or a JSON object wrapping one under
//     "links" or "subscription".
//
// parseStructured is tried BEFORE the base64/plain-text path in sub.go's
// Parse, because none of these shapes are ever base64-encoded in the wild —
// only the classic link list is — and JSON/YAML text would not decode as
// base64 anyway. Detection is cheap and ordered by how unambiguous the shape
// is: a JSON object/array is recognised by its first byte, Clash YAML only
// by a top-level `proxies:` list that actually unmarshals to something
// non-empty. Anything that matches neither falls through to sub.go's own
// base64/plain-text handling unchanged — that is what handled=false means
// below, as opposed to handled=true with a non-nil err for a shape we
// recognised but could not use (an outbounds array with no server we
// support, say): that must be reported as an error, not silently treated as
// an empty plain-text body.
package sub

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseStructured tries every structured subscription shape against text (a
// panel's response body, already trimmed) and reports the links it
// produced. handled is false when text matches none of them.
func parseStructured(text string) ([]string, error, bool) {
	if links, err, handled := parseJSON(text); handled {
		return links, err, true
	}
	if links, err, handled := parseClashYAML(text); handled {
		return links, err, true
	}
	return nil, nil, false
}

// parseJSON handles the JSON-shaped subscriptions: a sing-box config, a bare
// array of links, or an object wrapping one under "links"/"subscription". It
// only claims input whose first byte is '{' or '[' AND that is valid JSON —
// anything else (including a base64 blob, which never starts with either)
// is left to the caller's other detectors.
func parseJSON(text string) ([]string, error, bool) {
	if text == "" {
		return nil, nil, false
	}
	switch text[0] {
	case '{':
		if !json.Valid([]byte(text)) {
			return nil, nil, false
		}
		var head struct {
			Outbounds    json.RawMessage `json:"outbounds"`
			Links        json.RawMessage `json:"links"`
			Subscription json.RawMessage `json:"subscription"`
		}
		if err := json.Unmarshal([]byte(text), &head); err != nil {
			return nil, fmt.Errorf("sub: invalid JSON body: %w", err), true
		}
		switch {
		case head.Outbounds != nil:
			links, err := singboxOutbounds(head.Outbounds)
			return links, err, true
		case head.Links != nil:
			links, err := jsonLinksArray(head.Links, "links")
			return links, err, true
		case head.Subscription != nil:
			links, err := jsonLinksArray(head.Subscription, "subscription")
			return links, err, true
		default:
			return nil, fmt.Errorf("sub: JSON object has none of outbounds, links, or subscription"), true
		}
	case '[':
		if !json.Valid([]byte(text)) {
			return nil, nil, false
		}
		links, err := jsonLinksArray(json.RawMessage(text), "")
		return links, err, true
	default:
		return nil, nil, false
	}
}

// jsonLinksArray decodes a JSON array of link strings and keeps only the
// ones whose scheme sub.go's knownSchemes recognises, so one bad entry in
// the array shrinks the result instead of failing the whole subscription.
// field names the array in error messages ("links"/"subscription"); it is
// "" for the bare top-level array.
func jsonLinksArray(raw json.RawMessage, field string) ([]string, error) {
	var candidates []string
	if err := json.Unmarshal(raw, &candidates); err != nil {
		what := "JSON link array"
		if field != "" {
			what = fmt.Sprintf("JSON %q array", field)
		}
		return nil, fmt.Errorf("sub: invalid %s: %w", what, err)
	}
	links := filterKnownLinks(candidates)
	if len(links) == 0 {
		return nil, fmt.Errorf("sub: JSON array contained no share links we recognise")
	}
	return links, nil
}

// filterKnownLinks keeps only the strings that start with a scheme sub.go
// knows how to hand off to internal/protocol, discarding the rest.
func filterKnownLinks(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if loc := linkStart.FindStringIndex(s); loc != nil && loc[0] == 0 {
			out = append(out, s)
		}
	}
	return out
}

// parseClashYAML handles a Clash/Mihomo config: a top-level `proxies:` list.
// Unmarshaling text into clashDoc is how detection AND parsing happen in one
// step — text that is not actually Clash YAML (a base64 blob, a plain link
// list, or JSON already rejected by parseJSON) either fails to unmarshal
// into the struct or unmarshals with an empty Proxies list, and either way
// handled comes back false.
func parseClashYAML(text string) ([]string, error, bool) {
	var doc clashDoc
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil || len(doc.Proxies) == 0 {
		return nil, nil, false
	}
	links, err := clashLinks(doc.Proxies)
	return links, err, true
}
