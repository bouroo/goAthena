package app

import "crypto/md5" //nolint:gosec // rAthena use_MD5_passwords stores MD5 hex; protocol compat, not a security choice.

// md5sum returns the raw 16-byte MD5 digest, matching rAthena's
// use_MD5_passwords storage format. MD5 here is a compatibility requirement, not
// a cryptographic one (passwords are a 32-char hex legacy store).
//
//nolint:gosec // G401/G501: weak primitive is mandated by the rAthena wire/data format.
func md5sum(b []byte) [md5.Size]byte { return md5.Sum(b) }
