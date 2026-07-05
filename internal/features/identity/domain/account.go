package domain

import "time"

// Sex represents the rAthena `login.sex` column (enum('M','F','S')).
// The single-character encoding matches the schema verbatim so MySQL/Postgres
// repositories can round-trip without translation.
type Sex string

const (
	// SexMale is the standard male account sex.
	SexMale Sex = "M"
	// SexFemale is the standard female account sex.
	SexFemale Sex = "F"
	// SexServer marks server-side / system accounts; rAthena rejects login
	// attempts whose row sex is 'S' (login.cpp:350-353).
	SexServer Sex = "S"
)

// PasswordEncoding controls how `login.user_pass` is interpreted. The column
// is VARCHAR(32) and may hold plaintext OR MD5-hex depending on the
// deployment-wide `use_md5_passwds` bit (loginclif.cpp:279-281).
type PasswordEncoding uint8

const (
	// PassEncodingPlain stores the password in cleartext; the server compares
	// with strcmp directly (login.cpp:446).
	PassEncodingPlain PasswordEncoding = 0
	// PassEncodingMD5 stores the password as MD5(plaintext) lowercase hex
	// (32 chars); the client-supplied plaintext is MD5'd before compare
	// (loginclif.cpp:279-281).
	PassEncodingMD5 PasswordEncoding = 1
)

// AuthError is the wire-level AC_REFUSE_LOGIN code (loginclif.cpp:184-206).
// A value of 0 means success (login_mmo_auth returned -1). The numeric values
// are fixed by the network protocol and must not be reordered.
type AuthError uint8

const (
	// AuthOK indicates a successful authentication (login_mmo_auth returned -1).
	AuthOK AuthError = 0
	// AuthUnknownID indicates the account does not exist or the row is a
	// server/system row (login.cpp:347, 352). Shares the wire code 0 with
	// AuthOK because AuthOK only fires when login_mmo_auth returns -1.
	AuthUnknownID AuthError = 0
	// AuthInvalidPassword indicates the password did not match
	// (login_check_password returned false; login.cpp:357).
	AuthInvalidPassword AuthError = 1
	// AuthExpired indicates the account's expiration_time is in the past
	// (login.cpp:360-363).
	AuthExpired AuthError = 2
	// AuthRejected covers DNSBL hits, IP bans, the registration limit, and
	// the passwdenc/use_md5_passwds collision (login.cpp:315, 233).
	AuthRejected AuthError = 3
	// AuthGMBlocked is reserved by the legacy protocol; modern rAthena does
	// not raise it from the login flow itself.
	AuthGMBlocked AuthError = 4
	// AuthHashMismatch indicates the client's executable hash did not match
	// the configured value (login.cpp:398, 405).
	AuthHashMismatch AuthError = 5
	// AuthBanned indicates the account is currently banned; the
	// AC_REFUSE_LOGIN response populates the unblock_time field with the
	// unban_time value (login.cpp:365-370, loginclif.cpp:226-233).
	AuthBanned AuthError = 6
	// AuthServerJammed is reserved for population-cap refusals.
	AuthServerJammed AuthError = 7
	// AuthAlreadyOnline indicates the account is already logged in; raised
	// via SC_NOTIFY_BAN 0x0081, not AC_REFUSE_LOGIN (loginclif.cpp:97).
	AuthAlreadyOnline AuthError = 8
)

// Account represents a row in the rAthena `login` table. Field names mirror
// the SQL columns to keep the repository layer translation-free; zero values
// of the time fields mean "not set" (matching the SQL default of 0).
type Account struct {
	// AccountID is the numeric primary key (`account_id`).
	AccountID uint32
	// UserID is the login userid (`userid`), max NAME_LENGTH (23) bytes.
	UserID string
	// UserPass is the stored password, plaintext or MD5-hex depending on
	// the deployment's `use_md5_passwds` bit (`user_pass`).
	UserPass string
	// Email is the contact address (`email`), max 39 bytes.
	Email string
	// Sex is the account-level sex, copied onto characters that do not
	// override it (`sex`).
	Sex Sex
	// GroupID is the permission tier; 0 = player, 5 = VIP, 99 = admin
	// (`group_id`).
	GroupID uint8
	// State encodes a ban-like status; 0 = OK, non-zero = blocked and the
	// wire error is `state - 1` (login.cpp:372-375, account.hpp:26).
	State uint32
	// UnbanTime is the ban expiry timestamp; the zero value means
	// "not banned" (matches the SQL default of 0; `unban_time`).
	UnbanTime time.Time
	// ExpirationTime is the account expiry timestamp; the zero value means
	// "unlimited" (matches the SQL default of 0; `expiration_time`).
	ExpirationTime time.Time
	// LoginCount is incremented on every successful authentication
	// (`logincount`, login.cpp:423-424).
	LoginCount uint32
	// LastLogin is the timestamp of the most recent successful login
	// (`lastlogin`).
	LastLogin time.Time
	// LastIP is the textual address of the most recent successful login
	// (`last_ip`).
	LastIP string
	// Birthdate is the date of birth in YYYY-MM-DD form (`birthdate`).
	Birthdate string
	// CharacterSlots is the effective slot count for this account; 0 means
	// "fallback to MIN_CHARS" (`character_slots`).
	CharacterSlots uint8
	// WebAuthToken is the SSO token refreshed on successful login when
	// `use_web_auth_token` is enabled (`web_auth_token`, max 16 chars
	// truncated by LEFT()).
	WebAuthToken string
	// WebAuthTokenEnabled is a soft revoke flag; cleared
	// `disable_webtoken_delay` seconds after disconnect
	// (account.cpp:952-958).
	WebAuthTokenEnabled bool
	// VipTime is the VIP expiry timestamp (`vip_time`).
	VipTime time.Time
	// OldGroup is the pre-VIP group for rollback (`old_group`).
	OldGroup uint8
}
