package main

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		text     string
		category string
		lang     string
	}{
		{"❤️🔥", "emoji", "uz"},
		{"Assalomu alaykum", "greeting_islamic", "uz"},
		{"Alloh asrasin", "dua", "uz"},
		{"Check my profile www.spam.com follow me", "spam", "en"},
		{"Mashallah juda chiroyli", "praise", "uz"},
		{"Qayerda olingan?", "question", "uz"},
		{"Привет", "greeting", "ru"},
		{"Wow amazing video", "praise", "en"},
	}
	for _, c := range cases {
		got := Classify(c.text)
		if got.Category != c.category {
			t.Errorf("%q: kategoriya %q, kutilgan %q", c.text, got.Category, c.category)
		}
		if c.category != "spam" && got.Lang != c.lang {
			t.Errorf("%q: til %q, kutilgan %q", c.text, got.Lang, c.lang)
		}
	}
}

func TestSpamNotReplied(t *testing.T) {
	if Classify("obuna bo'ling t.me/kanal").Reply {
		t.Error("spamga javob berilmasligi kerak")
	}
}

func TestValidateReply(t *testing.T) {
	if validateReply("Rahmat https://x.com", "salom") == nil {
		t.Error("havolali javob rad etilishi kerak")
	}
	if validateReply("Rahmat 🤍", "salom") != nil {
		t.Error("oddiy javob qabul qilinishi kerak")
	}
}
