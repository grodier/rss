package password

import (
	"errors"
	"strings"
	"testing"
)

func TestHashVerify(t *testing.T) {
	h, err := Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("unexpected encoding %q", h)
	}

	ok, err := Verify("correct horse", h)
	if err != nil || !ok {
		t.Errorf("Verify correct = %v, %v; want true, nil", ok, err)
	}
	ok, err = Verify("wrong horse", h)
	if err != nil || ok {
		t.Errorf("Verify wrong = %v, %v; want false, nil", ok, err)
	}
}

func TestHashSalted(t *testing.T) {
	a, _ := Hash("same")
	b, _ := Hash("same")
	if a == b {
		t.Error("two hashes of the same password are identical")
	}
}

func TestLongMultibytePasswords(t *testing.T) {
	for name, pw := range map[string]string{
		"emoji": strings.Repeat("😀", 30),
		"cjk":   strings.Repeat("密码测试", 50),
	} {
		t.Run(name, func(t *testing.T) {
			h, err := Hash(pw)
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := Verify(pw, h); err != nil || !ok {
				t.Errorf("round trip = %v, %v; want true, nil", ok, err)
			}
			// Changing only the last character must not match.
			runes := []rune(pw)
			runes[len(runes)-1] = 'x'
			if ok, err := Verify(string(runes), h); err != nil || ok {
				t.Errorf("changed last char = %v, %v; want false, nil", ok, err)
			}
		})
	}
}

func TestVerifyOtherParams(t *testing.T) {
	p := current
	p.memory = 8 * 1024
	h, err := hashWith("pw", p)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := Verify("pw", h); err != nil || !ok {
		t.Errorf("Verify = %v, %v; want true, nil", ok, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	good, _ := Hash("pw")
	parts := strings.Split(good, "$")

	tests := map[string]string{
		"empty":        "",
		"prefix only":  "$argon2id$",
		"bad base64":   "$argon2id$v=19$m=19456,t=2,p=1$!!!$!!!",
		"bad version":  "$argon2id$v=18$m=19456,t=2,p=1$" + parts[4] + "$" + parts[5],
		"argon2i":      "$argon2i$v=19$m=19456,t=2,p=1$" + parts[4] + "$" + parts[5],
		"bad params":   "$argon2id$v=19$m=x,t=2,p=1$" + parts[4] + "$" + parts[5],
		"zero params":  "$argon2id$v=19$m=0,t=0,p=0$" + parts[4] + "$" + parts[5],
		"bcrypt":       "$2a$12$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234",
		"extra part":   good + "$x",
		"missing hash": "$argon2id$v=19$m=19456,t=2,p=1$" + parts[4] + "$",
	}
	for name, enc := range tests {
		t.Run(name, func(t *testing.T) {
			ok, err := Verify("pw", enc)
			if ok || !errors.Is(err, ErrInvalidHash) {
				t.Errorf("Verify = %v, %v; want false, ErrInvalidHash", ok, err)
			}
		})
	}
}
