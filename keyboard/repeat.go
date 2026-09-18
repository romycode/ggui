package keyboard

import (
	"regexp"
	"strings"
)

// Predicates are ordered by XKB specificity, from least to most specific.
type interpretMatch uint8

const (
	matchAnyOfOrNone interpretMatch = iota
	matchAnyOf
	matchNoneOf
	matchAllOf
	matchExactly
)

type repeatInterpret struct {
	sym    Keysym // zero is the Any/NoSymbol wildcard
	mods   uint32
	match  interpretMatch
	repeat bool
}

var (
	reRepeat    = regexp.MustCompile(`(?i)\brepeat\s*=\s*(yes|no|true|false|on|off)\b`)
	reIntRepeat = regexp.MustCompile(`(?i)\binterpret\s*\.\s*repeat\s*=\s*(?:yes|no|true|false|on|off)\s*;`)
	rePredicate = regexp.MustCompile(`(?i)^([a-z]+)\s*\(([^()]*)\)$`)
)

// resolveRepeats applies only the interpret for group 1, level 1. Explicit key
// settings take precedence; an unmatched key repeats, but a matching interpret
// inherits false unless an earlier interpret.repeat declaration changed it.
func (km *Keymap) resolveRepeats(sec string) {
	var rules []repeatInterpret
	defaults := reIntRepeat.FindAllStringIndex(sec, -1)
	defaultRepeat := false
	for _, loc := range reInterpret.FindAllStringSubmatchIndex(sec, -1) {
		for len(defaults) > 0 && defaults[0][0] < loc[0] {
			span := defaults[0]
			defaultRepeat, _ = parseRepeat(sec[span[0]:span[1]])
			defaults = defaults[1:]
		}
		name := sec[loc[2]:loc[3]]
		sym := ParseKeysym(name)
		if sym == 0 && !strings.EqualFold(name, "Any") && !isResolvedZeroKeysym(name) {
			continue
		}
		predicate := ""
		if loc[4] >= 0 {
			predicate = sec[loc[4]:loc[5]]
		}
		match, mods, ok := parseInterpretMatch(predicate)
		if !ok {
			continue
		}
		repeat, explicit := parseRepeat(sec[loc[6]:loc[7]])
		if !explicit {
			repeat = defaultRepeat
		}
		rules = append(rules, repeatInterpret{sym: sym, mods: mods, match: match, repeat: repeat})
	}
	for keycode, k := range km.keys {
		if k.repeatExplicit || len(k.groups) == 0 || len(k.groups[0]) == 0 {
			continue
		}
		if k.groups[0][0] == 0 {
			continue // NoSymbol levels do not receive an interpret, even Any.
		}
		best := -1
		for _, rule := range rules {
			if rule.sym != 0 && rule.sym != k.groups[0][0] {
				continue
			}
			if !rule.matches(km.modMap[keycode]) {
				continue
			}
			score := int(rule.match)
			if rule.sym != 0 {
				score += int(matchExactly) + 1
			}
			// Equal specificity retains the first matching declaration.
			if score > best {
				best = score
				k.repeat = rule.repeat
			}
		}
	}
}

func (r repeatInterpret) matches(mods uint32) bool {
	switch r.match {
	case matchAnyOfOrNone:
		return mods == 0 || mods&r.mods != 0
	case matchAnyOf:
		return mods&r.mods != 0
	case matchNoneOf:
		return mods&r.mods == 0
	case matchAllOf:
		return mods&r.mods == r.mods
	case matchExactly:
		return mods == r.mods
	default:
		return false
	}
}

func parseInterpretMatch(raw string) (interpretMatch, uint32, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return matchAnyOfOrNone, 0xff, true
	}
	if strings.EqualFold(raw, "Any") {
		return matchAnyOf, 0xff, true
	}
	match := matchExactly
	mask := raw
	if m := rePredicate.FindStringSubmatch(raw); m != nil {
		mask = m[2]
		switch strings.ToLower(m[1]) {
		case "anyofornone":
			match = matchAnyOfOrNone
		case "anyof":
			match = matchAnyOf
		case "noneof":
			match = matchNoneOf
		case "allof":
			match = matchAllOf
		case "exactly":
			match = matchExactly
		default:
			return 0, 0, false
		}
	}
	var mods uint32
	for _, part := range strings.Split(mask, "+") {
		part = strings.TrimSpace(part)
		if strings.EqualFold(part, "all") {
			mods |= 0xff
			continue
		}
		found := false
		for name, bit := range realMods {
			if strings.EqualFold(part, name) {
				mods |= bit
				found = true
				break
			}
		}
		if !found {
			return 0, 0, false
		}
	}
	return match, mods, true
}

func parseRepeat(s string) (bool, bool) {
	matches := reRepeat.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return false, false
	}
	value := matches[len(matches)-1][1]
	switch strings.ToLower(value) {
	case "yes", "true", "on":
		return true, true
	default:
		return false, true
	}
}
