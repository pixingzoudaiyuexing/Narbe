package cloud

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Linode documents 7-128 characters and a server-side strength score, without
// publishing its algorithm. Narbe keeps its 12-character floor and blocks obvious
// weak patterns; this does not promise acceptance by the provider's strength check.
func ValidateLinodePassword(password string) error {
	if password == "" {
		return nil
	}
	length := utf8.RuneCountInString(password)
	if length < 12 || length > 128 {
		return errors.New("root 密码长度必须为 12–128 字符")
	}
	letters := []rune(strings.ToLower(password))
	distinct := map[rune]bool{}
	var plain strings.Builder
	for _, r := range letters {
		if unicode.IsControl(r) {
			return errors.New("root 密码不能包含控制字符")
		}
		distinct[r] = true
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			plain.WriteRune(r)
		}
	}
	weak := len(distinct) < 4
	weak = weak || repeatedPasswordPattern(letters)
	stripped := plain.String()
	for _, sequence := range []string{"012345678901234567890123456789", "123456789012345678901234567890", "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz", "zyxwvutsrqponmlkjihgfedcbazyxwvutsrqponmlkjihgfedcba", "qwertyuiopasdfghjklzxcvbnm", "qazwsxedcrfvtgbyhnujmikolp"} {
		if len(stripped) >= 6 && strings.Contains(sequence, stripped) {
			weak = true
		}
	}
	weak = weak || repeatedPasswordPattern([]rune(stripped))
	// Common words decorated only with digits/symbols are still predictable.
	base := strings.Trim(stripped, "0123456789")
	decorated := strings.Trim(strings.ToLower(password), "0123456789!@#$%^&*()-_=+.,? ")
	normalized := strings.NewReplacer("@", "a", "0", "o", "1", "i", "3", "e", "4", "a", "5", "s", "$", "s", "7", "t").Replace(decorated)
	var onlyLetters strings.Builder
	for _, r := range []rune(stripped) {
		if unicode.IsLetter(r) {
			onlyLetters.WriteRune(r)
		}
	}
	for _, word := range []string{"password", "passwordpassword", "qwerty", "administrator", "adminadmin", "letmein", "changeme", "welcome", "iloveyou"} {
		if base == word || normalized == word || onlyLetters.String() == word {
			weak = true
		}
	}
	if weak {
		return errors.New("root 密码过于简单，请使用密码管理器生成独立密码或使用较长随机短语；云端还会检查密码强度")
	}
	return nil
}

func repeatedPasswordPattern(letters []rune) bool {
	for n := 1; n <= 12 && n <= len(letters)/2; n++ {
		if len(letters)%n != 0 {
			continue
		}
		repeated := true
		for i := n; i < len(letters); i++ {
			if letters[i] != letters[i%n] {
				repeated = false
				break
			}
		}
		if repeated {
			return true
		}
	}
	return false
}
