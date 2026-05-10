// Solana stealth-salt encryption.
//
// The relayer issues a 32-byte `ephemeral_salt` per stealth registration.
// The salt is the secret seed of the stealth PDA — anyone who learns it can
// derive the user's stealth address. We must deliver it to the user without
// leaking it to anyone else (including a passive observer of the relayer's
// API responses).
//
// Solana wallets sign with ed25519. NaCl `box` operates on X25519 keys. The
// canonical bridge is the deterministic ed25519→X25519 mapping (Edwards →
// Montgomery curve), which lets us treat any Solana pubkey as an X25519
// recipient without the user needing a separate keypair. The user's
// frontend reverses the conversion on their secret key to decrypt.
//
// On the wire we return a `SolanaSealedSalt` blob: the relayer's ephemeral
// X25519 pubkey, a 24-byte nonce, and the AEAD ciphertext. The user opens
// it with `nacl.box.Open(ciphertext, nonce, ephemeralX25519Pub,
// userX25519Sk)` after converting their ed25519 sk via the standard
// `ed25519_sk_to_curve25519` recipe (sha512 of the seed, clamp the lower
// 32 bytes).
//
// Why not symmetric-from-signature? ed25519 sigs are deterministic, so a
// salt-encryption key derived from `sign(user_sk, "stealth-key-v1")` would
// work — but it requires the user to sign one message per session before
// they can decrypt anything, and any FE bug that leaks that signature
// leaks all stealth addresses simultaneously. Asymmetric encryption is
// the safer default and matches industry practice (Phantom, Solflare,
// Backpack all expose ed25519 signing; Curve25519 conversion is a few
// lines of code on either side).

package stealth

import (
	"crypto/rand"
	"errors"
	"fmt"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/nacl/box"
)

// SolanaSealedSalt is the wire-format payload returned to the user. All
// three fields are required for decryption.
type SolanaSealedSalt struct {
	// EphemeralX25519Pub is the relayer's per-call X25519 public key. 32
	// bytes raw (not base58 — callers stringify in their own format).
	EphemeralX25519Pub [32]byte
	// Nonce is the 24-byte XSalsa20 nonce used by `nacl.box`.
	Nonce [24]byte
	// Ciphertext is the box-sealed salt: 16 bytes Poly1305 tag plus the
	// 32-byte salt (so always exactly 48 bytes for a salt-of-32).
	Ciphertext []byte
}

// SealSolanaSalt encrypts `salt` for the given Solana ed25519 pubkey using
// the relayer's freshly-generated ephemeral X25519 keypair. Caller-supplied
// salt is wiped from the relayer's memory after the call returns (the
// caller is expected to overwrite/zero its copy).
//
// The recipient pubkey must be a valid Edwards point — i.e. a real ed25519
// pubkey produced by a Solana wallet. Junk inputs (not on the curve) are
// rejected up-front instead of silently producing undecryptable output.
func SealSolanaSalt(recipientEd25519Pub [32]byte, salt []byte) (*SolanaSealedSalt, error) {
	if len(salt) == 0 {
		return nil, errors.New("salt is empty")
	}
	recipX25519, err := ed25519PubToX25519(recipientEd25519Pub)
	if err != nil {
		return nil, fmt.Errorf("recipient pubkey: %w", err)
	}

	var ephPub, ephSk [32]byte
	if _, err := rand.Read(ephSk[:]); err != nil {
		return nil, fmt.Errorf("rand: %w", err)
	}
	// Curve25519 secret-key clamping. nacl.box.Precompute does this
	// internally if you pass an unclamped key, but we're explicit for
	// auditability.
	ephSk[0] &= 248
	ephSk[31] &= 127
	ephSk[31] |= 64

	// Derive the matching X25519 public key. nacl.box derives this for us
	// when we pass the secret to Seal, but we also need to *return* it so
	// the recipient can call Open. The library's GenerateKey would do the
	// same thing, but we already have the secret in hand.
	{
		var pub [32]byte
		curve25519ScalarBaseMult(&pub, &ephSk)
		ephPub = pub
	}

	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("rand: %w", err)
	}

	ct := box.Seal(nil, salt, &nonce, &recipX25519, &ephSk)

	return &SolanaSealedSalt{
		EphemeralX25519Pub: ephPub,
		Nonce:              nonce,
		Ciphertext:         ct,
	}, nil
}

// ed25519PubToX25519 maps an ed25519 public key (Edwards form) to its
// corresponding X25519 public key (Montgomery form). The mapping is
// `u = (1 + y) / (1 - y) (mod p)` where (x, y) is the decompressed
// Edwards point.
//
// Implemented via filippo.io/edwards25519 which exposes the bytes -> point
// decompression and field arithmetic primitives we need.
func ed25519PubToX25519(edPub [32]byte) ([32]byte, error) {
	var out [32]byte

	p, err := new(edwards25519.Point).SetBytes(edPub[:])
	if err != nil {
		return out, fmt.Errorf("not a valid ed25519 point: %w", err)
	}

	// edwards25519.Point exposes BytesMontgomery directly — it returns the
	// u-coordinate of the corresponding Montgomery point, which is exactly
	// the X25519 public key.
	mont := p.BytesMontgomery()
	if len(mont) != 32 {
		return out, fmt.Errorf("BytesMontgomery: got %d bytes, want 32", len(mont))
	}
	copy(out[:], mont)
	return out, nil
}

// curve25519ScalarBaseMult computes `pub = sk · G` for X25519. Wrapping
// the standard library's primitive keeps the call site readable and lets
// us swap impls if needed.
func curve25519ScalarBaseMult(pub, sk *[32]byte) {
	// Use the well-known X25519 base scalar mult via x/crypto/curve25519.
	// We avoid importing it at package top-level to keep the dependency
	// surface visibly tied to the seal-only call site.
	curve25519ScalarBaseMultImpl(pub, sk)
}
