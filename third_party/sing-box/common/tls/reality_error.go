package tls

// RealityRejectionError means that the remote endpoint completed TLS but did
// not authenticate the connection as a REALITY client.
type RealityRejectionError struct{}

func (*RealityRejectionError) Error() string {
	return "REALITY: server rejected client (check Xray minClientVer, SNI, public key, and short ID)"
}

func (*RealityRejectionError) Temporary() bool { return false }
