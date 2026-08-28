package resources

import (
	"context"
	"encoding/json"
	"strings"
)

// bindMountFlags are the docker-compose style suffixes that may appear on
// volume Source/Target strings in authored configuration
// ("/etc/localtime:ro"). Exports never use this form: they carry structured
// mounts with an explicit ReadOnly boolean.
var bindMountFlags = map[string]bool{
	"ro":         true,
	"rw":         true,
	"z":          true, // SELinux relabel flags
	"Z":          true,
	"consistent": true,
	"delegated":  true,
	"cached":     true,
	"nocopy":     true,
}

// normalizeVolumeEntry canonicalizes a single volume entry so the two
// syntactic forms compare equal:
//
//	config:  {"Type":"bind","Source":"/etc/localtime:ro","Target":"/etc/localtime:ro"}
//	export:  {"Type":"bind","Source":"/etc/localtime","Target":"/etc/localtime","ReadOnly":true}
//
// Rules applied to both sides:
//   - Source/Target strings are split on ":" — a trailing flag token that is
//     a known mount flag is stripped and folded into ReadOnly (ro=true,
//     rw=false). Only the LAST colon segment is considered, and only when it
//     is a known flag, so paths containing colons (none sane in mounts) and
//     named volumes with registry ports are not mangled.
//   - A missing ReadOnly key is equivalent to false.
//   - Missing/nil scalar fields are equivalent to empty strings.
//
// Returns nil if the entry is not an object (callers fall back to strict
// comparison for those).
func normalizeVolumeEntry(v interface{}) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	out := make(map[string]interface{}, len(m))
	for k, val := range m {
		out[k] = val
	}
	for _, field := range []string{"Source", "Target"} {
		s, ok := out[field].(string)
		if !ok || s == "" {
			continue
		}
		if idx := strings.LastIndex(s, ":"); idx > 0 {
			flag := s[idx+1:]
			if bindMountFlags[flag] {
				s = s[:idx]
				if flag == "ro" {
					out["ReadOnly"] = true
				} else if flag == "rw" {
					if _, exists := out["ReadOnly"]; !exists {
						out["ReadOnly"] = false
					}
				}
			}
		}
		out[field] = s
	}
	// nil == absent for ReadOnly
	if ro, exists := out["ReadOnly"]; exists && ro == nil {
		delete(out, "ReadOnly")
	}
	// normalize missing scalar keys to empty strings so absent vs "" compares equal
	for _, field := range []string{"Type", "Source", "Target", "Name", "Driver", "Mode", "Propagation"} {
		if val, exists := out[field]; !exists || val == nil {
			out[field] = ""
		}
	}
	// ReadOnly: absent -> false
	if _, exists := out["ReadOnly"]; !exists {
		out["ReadOnly"] = false
	}
	return out
}

// volumesEqual compares two "volumes" arrays: entries are canonicalized via
// normalizeVolumeEntry, then matched as a multiset (mount order is not
// stable across Docker inspects).
func volumesEqual(a, b []interface{}) bool {
	na := make([]interface{}, len(a))
	nb := make([]interface{}, len(b))
	for i, v := range a {
		na[i] = normalizeVolumeEntry(v)
	}
	for i, v := range b {
		nb[i] = normalizeVolumeEntry(v)
	}
	return listMultisetEqual(na, nb)
}

// marshalCompact is a small helper used by tests and normalization paths.
func marshalCompact(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

var _ = context.Background
