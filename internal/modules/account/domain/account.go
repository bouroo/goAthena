// Package domain defines the account bounded context's value objects and
// outbound ports: the account aggregate (auth slice only), the session minted
// at CA_LOGIN, and the repository / session-store ports the application layer
// depends on. It is pure — no transport or persistence dependencies — so the
// GORM and in-memory adapters, the AuthService, and tests all program against
// these types rather than against each other.
package domain

import (
	"errors"
	"time"
)

// Sex mirrors the login.sex enum('M','F','S') so repositories round-trip the
// column without translation. rAthena rejects server accounts ('S') at login
// (login.cpp:350-353).
type Sex string

// The values are the login.sex enum literals, so repositories persist them
// verbatim and no mapping layer is needed at the storage boundary.
const (
	SexMale   Sex = "M"
	SexFemale Sex = "F"
	SexServer Sex = "S"
)

// WireByte returns the numeric e_sex sent in AC_ACCEPT_LOGIN, per sex_str2num
// (login.hpp:128): 'F' → 0 (SEX_FEMALE), 'M' → 1 (SEX_MALE), anything else →
// 3 (SEX_SERVER). SEX_BOTH (2) is never assigned to an account (mmo.hpp:1115).
func (s Sex) WireByte() uint8 {
	switch s {
	case SexFemale:
		return 0
	case SexMale:
		return 1
	default:
		return 3
	}
}

// Account is the auth slice of a login-table row: exactly the fields the
// CA_LOGIN use case reads or mutates. Time fields use unix seconds (matching
// the int(11) SQL columns) with 0 meaning "not set", the SQL default; only
// LastLogin is a time.Time because the column is a datetime.
type Account struct {
	AccountID      uint32
	UserID         string
	UserPass       string
	Sex            Sex
	State          uint32
	UnbanTime      int64 // unix seconds; 0 = no active ban
	ExpirationTime int64 // unix seconds; 0 = never expires
	WebAuthToken   string
	LoginCount     uint32
	LastIP         string
	LastLogin      time.Time
}

// Sentinel errors returned by the ports. Service code compares with errors.Is
// so wrapping is preserved; repository adapters must return these (wrapped)
// rather than their own driver-specific not-found types.
var (
	// ErrAccountNotFound: no login row matched the lookup key.
	ErrAccountNotFound = errors.New("account not found")
	// ErrSessionNotFound: no session exists for the account id.
	ErrSessionNotFound = errors.New("session not found")
)
