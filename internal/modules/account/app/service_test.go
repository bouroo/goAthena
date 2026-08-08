//go:build unit

package app_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/account/app"
	"github.com/bouroo/goAthena/internal/modules/account/domain"
	"github.com/bouroo/goAthena/internal/modules/account/infra"
)

func md5hex(s string) string {
	d := md5.Sum([]byte(s))
	return hex.EncodeToString(d[:])
}

func TestAuthenticate_MD5Success(t *testing.T) {
	repo := infra.NewMemoryAccountRepository(domain.Account{
		ID: 2000000, UserID: "alice", UserPass: md5hex("s3cret"), Sex: domain.SexFemale,
	})
	svc := app.NewAuthService(repo, true) // use_MD5_passwords

	acc, id1, id2, err := svc.Authenticate(context.Background(), "alice", "s3cret", "1.2.3.4")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if acc.ID != 2000000 {
		t.Errorf("account id = %d, want 2000000", acc.ID)
	}
	if id1 == 0 && id2 == 0 {
		t.Error("session tokens both zero; expected random non-zero pair")
	}
}

func TestAuthenticate_PlaintextSuccess(t *testing.T) {
	repo := infra.NewMemoryAccountRepository(domain.Account{
		ID: 1, UserID: "bob", UserPass: "hunter2", Sex: domain.SexMale,
	})
	svc := app.NewAuthService(repo, false) // plaintext mode

	if _, _, _, err := svc.Authenticate(context.Background(), "bob", "hunter2", ""); err != nil {
		t.Fatalf("plaintext authenticate: %v", err)
	}
}

func TestAuthenticate_WrongPassword(t *testing.T) {
	repo := infra.NewMemoryAccountRepository(domain.Account{
		ID: 1, UserID: "bob", UserPass: "hunter2",
	})
	svc := app.NewAuthService(repo, false)

	_, _, _, err := svc.Authenticate(context.Background(), "bob", "wrong", "")
	if !errors.Is(err, domain.ErrInvalidPassword) {
		t.Errorf("err = %v, want ErrInvalidPassword", err)
	}
}

func TestAuthenticate_UnknownAccount(t *testing.T) {
	repo := infra.NewMemoryAccountRepository()
	svc := app.NewAuthService(repo, false)

	_, _, _, err := svc.Authenticate(context.Background(), "nobody", "x", "")
	if !errors.Is(err, domain.ErrAccountNotFound) {
		t.Errorf("err = %v, want ErrAccountNotFound", err)
	}
}

func TestAuthenticate_BannedRejected(t *testing.T) {
	repo := infra.NewMemoryAccountRepository(domain.Account{
		ID: 1, UserID: "banned", UserPass: "p", State: 1, // state != 0 -> banned
	})
	svc := app.NewAuthService(repo, false)

	_, _, _, err := svc.Authenticate(context.Background(), "banned", "p", "")
	if !errors.Is(err, domain.ErrAccountBanned) {
		t.Errorf("err = %v, want ErrAccountBanned", err)
	}
}

func TestAuthenticate_RecordLoginBumpsCount(t *testing.T) {
	acc := domain.Account{ID: 1, UserID: "bob", UserPass: "hunter2"}
	repo := infra.NewMemoryAccountRepository(acc)
	svc := app.NewAuthService(repo, false)

	if _, _, _, err := svc.Authenticate(context.Background(), "bob", "hunter2", "5.6.7.8"); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	got, err := repo.FindByUserID(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	if got.LoginCount != 1 {
		t.Errorf("logincount = %d, want 1", got.LoginCount)
	}
	if got.LastIP != "5.6.7.8" {
		t.Errorf("last_ip = %q, want 5.6.7.8", got.LastIP)
	}
}
