package platform

import "testing"

func TestIoregUUIDPattern_Extracts(t *testing.T) {
	sample := []byte(`+-o IOPlatformExpertDevice  <class IOPlatformExpertDevice, id 0x100000100, retain 35>
    {
      "IOPolledInterface" = "SMCPolledInterface is not serializable"
      "IOPlatformUUID" = "4912B977-ACFB-5799-89A4-618042726226"
      "IOBusyInterest" = "IOCommand is not serializable"
    }
`)
	m := ioregUUIDPattern.FindSubmatch(sample)
	if m == nil {
		t.Fatal("expected a match")
	}
	if got := string(m[1]); got != "4912B977-ACFB-5799-89A4-618042726226" {
		t.Errorf("got %q", got)
	}
}

func TestIoregUUIDPattern_NoMatch(t *testing.T) {
	if m := ioregUUIDPattern.FindSubmatch([]byte("no uuid here")); m != nil {
		t.Errorf("expected no match, got %v", m)
	}
}

// TestDeviceID_StableAndHashed runs the real ioreg command (this test only
// makes sense on macOS, which is the only OS singctl targets) and checks the
// result is a stable, opaque SHA-256 hex digest — never the raw UUID.
func TestDeviceID_StableAndHashed(t *testing.T) {
	id := DeviceID()
	if id == "" {
		t.Skip("could not read IOPlatformUUID in this environment (e.g. sandboxed CI)")
	}
	if len(id) != 64 {
		t.Errorf("DeviceID() = %q, want 64 hex chars (sha256)", id)
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("DeviceID() = %q, contains non-lowercase-hex char %q", id, c)
			break
		}
	}
	if id2 := DeviceID(); id2 != id {
		t.Errorf("DeviceID() not stable across calls: %q vs %q", id, id2)
	}
	if uuid := platformUUID(); uuid != "" && id == uuid {
		t.Error("DeviceID() must not equal the raw platform UUID")
	}
}
