//go:build linux

package clashapi

import "testing"

// A trimmed real /proc/net/tcp sample: header + two rows. Local port 0x1F90 =
// 8080 (inode 12345), 0xD431 = 54321 (inode 67890).
const procNetTCPFixture = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0100007F:D431 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 67890 1 0000000000000000 20 4 30 10 -1
`

func TestParseProcNetTCP(t *testing.T) {
	m := parseProcNetTCP([]byte(procNetTCPFixture))
	if got := m[8080]; got != 12345 {
		t.Errorf("port 8080 inode = %d, want 12345", got)
	}
	if got := m[54321]; got != 67890 {
		t.Errorf("port 54321 inode = %d, want 67890", got)
	}
	if _, ok := m[9999]; ok {
		t.Errorf("unexpected entry for port 9999")
	}
}

func TestParseProcNetTCP_IgnoresGarbage(t *testing.T) {
	m := parseProcNetTCP([]byte("header\nnot a valid row\n: :\n"))
	if len(m) != 0 {
		t.Errorf("expected no entries from garbage, got %v", m)
	}
}
