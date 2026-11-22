package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// truncate matnni n ta belgigacha qisqartiradi.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// cleanText bazaga yozishdan oldin xavfli belgilarni olib tashlaydi.
func cleanText(s string) string {
	return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "")
}

// cleanErr tarmoq xatosidagi URL'ni (ichida token bo'lishi mumkin) olib tashlaydi.
func cleanErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("tarmoq xatosi (%s): %v", ue.Op, ue.Err)
	}
	return err
}

// validateReply tayyor javobni yuborishdan oldin tekshiradi.
func validateReply(reply, comment string) error {
	r := strings.TrimSpace(reply)
	if r == "" {
		return errors.New("bo'sh javob")
	}
	if utf8.RuneCountInString(r) > 280 {
		return errors.New("javob juda uzun")
	}
	low := strings.ToLower(r)
	for _, bad := range []string{"http", "www.", ".com", "t.me", "@"} {
		if strings.Contains(low, bad) {
			return fmt.Errorf("javobda taqiqlangan belgi bor: %s", bad)
		}
	}
	if strings.EqualFold(r, strings.TrimSpace(comment)) {
		return errors.New("javob kommentga aynan o'xshash")
	}
	return nil
}
