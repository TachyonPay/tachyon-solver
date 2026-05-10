package stealth

import "golang.org/x/crypto/curve25519"

// curve25519ScalarBaseMultImpl is split into its own file so the import of
// `golang.org/x/crypto/curve25519` is colocated with its single use site.
func curve25519ScalarBaseMultImpl(pub, sk *[32]byte) {
	out, err := curve25519.X25519(sk[:], curve25519.Basepoint)
	if err != nil {
		// X25519 only fails on length errors; ours is always 32. Panic so
		// any caller passing the wrong size sees it immediately.
		panic("stealth: curve25519 base mult: " + err.Error())
	}
	copy(pub[:], out)
}
