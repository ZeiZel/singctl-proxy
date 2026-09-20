package protocol

import (
	"errors"
	"strings"
)

// ErrNoProfiles is returned by ParseAll when nothing was parsed — the input
// was empty, or every element was blank once trimmed. Mirrors the historical
// "no link provided" error internal/link used to return.
var ErrNoProfiles = errors.New("no link provided")

// ParseAll parses a batch of raw inputs into a []Profile, preserving order
// (order is failover priority downstream: index 0 is primary). Each raws[i]
// may itself be a blob containing several share links pasted together
// (newline/space/semicolon-separated), so a multi-line paste or a repeated
// --key flag both work.
//
// The one subtlety: a config-input protocol (WireGuard's INI file, say) is
// itself multi-line, so naively splitting every input on whitespace would
// shred it into garbage tokens. ParseAll therefore asks the config sniffers
// first, for the WHOLE (trimmed) string — if one of them claims it, that
// string is exactly one profile and is never split. Only when no config
// module claims the whole blob is it split into individual share-link
// tokens.
func (r *Registry) ParseAll(raws []string) ([]Profile, error) {
	var out []Profile
	for _, raw := range raws {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if r.isWholeConfig(trimmed) {
			p, err := r.Parse(trimmed)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
			continue
		}
		for _, tok := range r.splitLinks(trimmed) {
			p, err := r.Parse(tok)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, ErrNoProfiles
	}
	return out, nil
}

// isWholeConfig reports whether some registered InputConfig module claims
// raw in its entirety — asked BEFORE any share-link splitting so a
// multi-line config is never shredded.
func (r *Registry) isWholeConfig(raw string) bool {
	for _, m := range r.configs {
		if m.(Sniffer).Sniff(raw) {
			return true
		}
	}
	return false
}

// splitLinks breaks a blob into individual share links by finding where each
// one STARTS, and treating everything up to the next start as one link.
//
// It must not split on whitespace, which is the obvious-looking approach and
// is wrong: a link's #fragment is a display label chosen by the panel, and real
// ones contain spaces and punctuation — "#Poland 🇵🇱", "#Auto → [🚀 Optimal]".
// Whitespace splitting turns one such link into a valid link plus a handful of
// label shards, and the first shard ("→") then reaches the parser as a key,
// which is exactly the "unsupported protocol" error a real subscription
// produced. Anchoring on the next scheme sidesteps separators entirely:
// newline, space, comma or semicolon between links all work, and none of them
// can corrupt a label.
func (r *Registry) splitLinks(raw string) []string {
	if r.linkRe == nil {
		return []string{raw}
	}
	idx := r.linkRe.FindAllStringIndex(raw, -1)
	if len(idx) == 0 {
		// Nothing that looks like a link: hand the whole thing to Parse so the
		// caller gets the proper "unsupported input" message rather than silence.
		return []string{raw}
	}
	out := make([]string, 0, len(idx))
	for i, loc := range idx {
		end := len(raw)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		// Trim only the separators that trailed this link; a label's own inner
		// spaces are inside the slice and stay untouched.
		if tok := strings.TrimRight(strings.TrimSpace(raw[loc[0]:end]), ",;\r\n\t "); tok != "" {
			out = append(out, tok)
		}
	}
	return out
}
