package firebase

import (
	"context"
	"errors"
	"testing"

	"firebase.google.com/go/v4/auth"
	"github.com/Nidka/BPM-service/internal/features/mail/application"
)

type fakeClient struct{ err error }

func (f fakeClient) EmailVerificationLink(context.Context, string) (string, error) {
	return "https://x/?mode=verifyEmail&oobCode=A", f.err
}
func (f fakeClient) PasswordResetLink(context.Context, string) (string, error) {
	return "https://x/?mode=resetPassword&oobCode=A", f.err
}

func TestLinkErrorsAreTranslatedWithoutLeakingTheAddress(t *testing.T) {
	links := &ActionLinks{client: fakeClient{}}
	if link, err := links.PasswordResetLink(context.Background(), "a@b.pe"); err != nil || link == "" {
		t.Fatal(err)
	}
	if _, err := (&ActionLinks{client: fakeClient{err: errors.New("error con a@b.pe")}}).VerificationLink(context.Background(), "a@b.pe"); err == nil || err.Error() == "error con a@b.pe" || errors.Is(err, application.ErrAccountNotFound) {
		t.Fatal("error genérico esperado", err)
	}
	if auth.IsEmailNotFound(nil) {
		t.Fatal("sanidad")
	}
}
