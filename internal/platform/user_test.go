package platform

import (
	"errors"
	"os/user"
	"testing"
)

func TestResolveUser_FromSudoUser(t *testing.T) {
	env := func(k string) string {
		if k == "SUDO_USER" {
			return "mikhail"
		}
		return ""
	}
	lookup := func(name string) (*user.User, error) {
		if name != "mikhail" {
			t.Fatalf("looked up %q, want mikhail", name)
		}
		return &user.User{Username: "mikhail", Uid: "501", Gid: "20", HomeDir: "/Users/mikhail"}, nil
	}
	ru, err := ResolveUser(env, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if ru.Uid != 501 || ru.Gid != 20 || ru.HomeDir != "/Users/mikhail" || ru.Username != "mikhail" {
		t.Errorf("got %+v", ru)
	}
}

func TestResolveUser_FallsBackToUSER(t *testing.T) {
	env := func(k string) string {
		if k == "USER" {
			return "bob"
		}
		return ""
	}
	lookup := func(string) (*user.User, error) {
		return &user.User{Username: "bob", Uid: "1000", Gid: "1000", HomeDir: "/home/bob"}, nil
	}
	ru, err := ResolveUser(env, lookup)
	if err != nil || ru.Username != "bob" {
		t.Fatalf("got %+v err=%v", ru, err)
	}
}

func TestResolveUser_RootOrEmptyErrors(t *testing.T) {
	for _, name := range []string{"", "root"} {
		env := func(k string) string {
			if k == "SUDO_USER" {
				return name
			}
			return ""
		}
		if _, err := ResolveUser(env, func(string) (*user.User, error) { return nil, nil }); err == nil {
			t.Errorf("name %q should error", name)
		}
	}
}

func TestResolveUser_LookupError(t *testing.T) {
	env := func(k string) string {
		if k == "SUDO_USER" {
			return "ghost"
		}
		return ""
	}
	lookup := func(string) (*user.User, error) { return nil, errors.New("unknown user") }
	if _, err := ResolveUser(env, lookup); err == nil {
		t.Error("expected lookup error")
	}
}
