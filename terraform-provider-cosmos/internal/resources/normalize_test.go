package resources

import (
	"encoding/json"
	"testing"
)

func TestVolumesEqualRoSuffixVsReadOnly(t *testing.T) {
	config := []interface{}{
		map[string]interface{}{"Type": "bind", "Source": "/etc/localtime:ro", "Target": "/etc/localtime:ro"},
	}
	export := []interface{}{
		map[string]interface{}{"Type": "bind", "Source": "/etc/localtime", "Target": "/etc/localtime", "ReadOnly": true},
	}
	if !volumesEqual(config, export) {
		t.Fatalf("ro-suffix form should equal structured ReadOnly form")
	}
}

func TestVolumesEqualReordered(t *testing.T) {
	a := []interface{}{
		map[string]interface{}{"Type": "volume", "Source": "data", "Target": "/data"},
		map[string]interface{}{"Type": "bind", "Source": "/etc/localtime:ro", "Target": "/etc/localtime:ro"},
	}
	b := []interface{}{
		map[string]interface{}{"Type": "bind", "Source": "/etc/localtime", "Target": "/etc/localtime", "ReadOnly": true},
		map[string]interface{}{"Type": "volume", "Source": "data", "Target": "/data"},
	}
	if !volumesEqual(a, b) {
		t.Fatalf("reordered volumes should be equal")
	}
}

func TestVolumesNotEqualRealDrift(t *testing.T) {
	a := []interface{}{
		map[string]interface{}{"Type": "bind", "Source": "/etc/localtime:ro", "Target": "/etc/localtime:ro"},
	}
	b := []interface{}{
		map[string]interface{}{"Type": "bind", "Source": "/etc/other", "Target": "/etc/other", "ReadOnly": true},
	}
	if volumesEqual(a, b) {
		t.Fatalf("different sources must not be equal")
	}
}

func TestVolumesRwSuffix(t *testing.T) {
	a := []interface{}{
		map[string]interface{}{"Type": "bind", "Source": "/data:rw", "Target": "/data"},
	}
	b := []interface{}{
		map[string]interface{}{"Type": "bind", "Source": "/data", "Target": "/data", "ReadOnly": false},
	}
	if !volumesEqual(a, b) {
		t.Fatalf("rw suffix should equal ReadOnly=false")
	}
}

func TestNormalizeVolumeEntryPortInVolumeName(t *testing.T) {
	// named volume / host paths with colons must not be mangled unless the
	// last segment is a known flag
	e := map[string]interface{}{"Type": "volume", "Source": "host:5000/data", "Target": "/data"}
	n := normalizeVolumeEntry(e).(map[string]interface{})
	if n["Source"] != "host:5000/data" {
		t.Fatalf("source mangled: %v", n["Source"])
	}
}

func TestServiceJSONEquivalentNetworkModeNameVsID(t *testing.T) {
	// network_mode equality is handled at READ time (needs API), so the
	// comparator only needs to not regress on identical values
	a := map[string]interface{}{"network_mode": "container:gluetun", "image": "x"}
	b := map[string]interface{}{"network_mode": "container:gluetun", "image": "x"}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if !serviceJSONEquivalent(string(ja), string(jb)) {
		t.Fatalf("identical docs should be equivalent")
	}
	if serviceJSONEquivalent(`{"network_mode":"container:aaa"}`, `{"network_mode":"container:bbb"}`) {
		t.Fatalf("different network modes must NOT be equal")
	}
}
