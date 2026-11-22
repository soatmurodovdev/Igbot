package main

import (
	"regexp"
	"strings"
	"unicode"
)

// Result komment tasnifi natijasi.
type Result struct {
	Category string // emoji, praise, dua, greeting, greeting_islamic, question, criticism, long, other, spam, abuse, empty, special
	Lang     string // uz, ru, en
	Reply    bool   // javob berish kerakmi
	Reason   string // javob berilmasa, sababi
}

var (
	reURL     = regexp.MustCompile(`(https?://|www\.|t\.me/|wa\.me/|bit\.ly|\.com\b|\.ru\b|\.uz\b|\.org\b|\.net\b)`)
	rePhone   = regexp.MustCompile(`\+?\d[\d\s\-()]{8,}\d`)
	reMention = regexp.MustCompile(`@[\w.]+`)
)

var promoStems = []string{
	"obuna bo'l", "follow me", "follow back", "f4f", "sub4sub", "l4l", "dm me", "dm qiling",
	"direktga", "lichkaga", "в директ", "в лс", "подпиш", "заработ", "crypto", "kripto",
	"casino", "kazino", "казино", "1xbet", "promo", "bonus", "telegram kanal", "telegramga",
	"sahifamga", "profilimga", "link in bio", "check my", "check out my", "earn money",
	"pul ishla", "daromad", "работа на дому", "ставки", "usdt",
}

var abuseStems = []string{
	"ahmoq", "tentak", "iflos", "haromi", "mal'un", "dalbayob",
	"хуй", "сука", "блять", "блядь", "пидор", "мразь", "дебил", "идиот", "тварь",
	"fuck", "shit", "bitch", "asshole", "bastard", "idiot", "stupid", "moron",
}

var criticismStems = []string{
	"yoqmadi", "yoqmayapti", "yomon", "bema'ni", "bekor", "past sifat", "yolg'on", "aldov",
	"алдов", "не понрав", "плохо", "ужас", "разочар", "отстой", "обман", "ложь", "фигня",
	"disappoint",
}

var criticismWords = []string{"bad", "worst", "fake", "scam", "boring", "awful", "terrible", "hate", "trash", "rubbish"}

var islamicGreetStems = []string{"alaykum", "alejkum", "алейкум", "алайкум", "assalom", "asalom", "ассалом", "асалом", "assalamu"}

var greetWords = []string{"salom", "salam", "hello", "hi", "hey", "hii", "hola", "привет", "здравствуйте", "здравствуй", "салом", "добрый"}

var greetStems = []string{"добрый день", "доброе утро", "добрый вечер", "good morning", "good evening", "xayrli tong", "xayrli kun", "xayrli kech"}

var mashStems = []string{"mashalla", "mashaall", "masha'all", "masha'al", "mashaal", "машалла", "машаалла", "машаллах"}

var duaWords = []string{"omin", "amin", "amen", "ameen", "омин", "амин", "бог", "боже", "god"}

var duaStems = []string{
	"alloh", "olloh", "allah", "аллох", "оллох", "аллах", "ilohim", "илохим", "ilohi", "barik",
	"barakalla", "баракалла", "baraka", "asrasin", "асрасин", "insha", "иншалла", "иншаалла",
	"god bless", "дай бог", "храни",
}

var praiseWords = []string{"zor", "alo", "wow", "top", "klass", "super", "супер", "класс", "love", "nice", "cool", "great", "good", "best"}

var praiseStems = []string{
	"chiroyli", "ajoyib", "zo'r", "yoqdi", "ofarin", "a'lo", "ideal", "yaxshi", "omad", "baxt",
	"sog'liq", "tilay", "tabrik", "muborak",
	"красив", "круто", "отличн", "прекрасн", "молодец", "шикарн", "восхитит", "обожаю", "нравится",
	"успех", "здоровья", "счастья", "желаю", "поздравля",
	"beautiful", "amazing", "awesome", "perfect", "gorgeous", "well done", "congrat", "wish", "happy",
}

var questionAnywhere = map[string]bool{
	"narxi": true, "narx": true, "qancha": true, "qayerda": true, "qayerdan": true, "qachon": true,
	"necha": true, "manzil": true, "цена": true, "сколько": true, "где": true, "когда": true,
	"price": true, "where": true, "when": true, "location": true, "address": true, "адрес": true,
}

var questionFirst = map[string]bool{
	"kim": true, "nima": true, "qanday": true, "nega": true, "nechun": true, "qaysi": true, "qayer": true,
	"qayerga": true, "nechi": true, "kimning": true,
	"как": true, "почему": true, "кто": true, "что": true, "какой": true, "откуда": true,
	"who": true, "how": true, "what": true, "why": true, "which": true,
}

var enWords = map[string]bool{
	"i": true, "you": true, "the": true, "is": true, "are": true, "and": true, "this": true, "that": true,
	"love": true, "so": true, "very": true, "nice": true, "great": true, "what": true, "how": true,
	"where": true, "when": true, "who": true, "why": true, "wow": true, "amazing": true, "beautiful": true,
	"thanks": true, "thank": true, "good": true, "best": true, "my": true, "me": true, "we": true,
	"it": true, "of": true, "to": true, "in": true, "for": true, "with": true, "awesome": true,
	"cool": true, "perfect": true, "hello": true, "hi": true, "hey": true, "price": true,
	"bad": true, "worst": true, "fake": true, "congratulations": true,
}

var uzWords = map[string]bool{
	"va": true, "bu": true, "juda": true, "uchun": true, "rahmat": true, "yaxshi": true, "chiroyli": true,
	"zo'r": true, "alloh": true, "omin": true, "salom": true, "qayerda": true, "qachon": true, "necha": true,
	"nima": true, "kim": true, "qanday": true, "men": true, "siz": true, "sizga": true, "bizga": true,
	"bor": true, "yo'q": true, "ham": true, "mashallah": true, "ajoyib": true, "yoqdi": true,
	"assalomu": true, "alaykum": true, "barakalla": true, "ilohim": true, "kerak": true, "lekin": true,
	"narxi": true, "qancha": true, "tabriklayman": true,
}

var uzCyrStems = []string{"илохим", "омин", "аллох", "оллох", "чиройли", "рахмат", "ассалому", "салом алайкум"}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	r := strings.NewReplacer("’", "'", "ʻ", "'", "ʼ", "'", "`", "'", "‘", "'", "´", "'")
	return r.Replace(s)
}

func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'')
	})
}

// analyze: harflar, raqamlar, kirill harflari va belgilar (emoji) sonini sanaydi.
func analyze(s string) (letters, digits, cyrillic, symbols int) {
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letters++
			if unicode.Is(unicode.Cyrillic, r) {
				cyrillic++
			}
		case unicode.IsDigit(r):
			digits++
		case unicode.IsSymbol(r) || r >= 0x1F000:
			symbols++
		}
	}
	return
}

func hasWord(tokens []string, words ...string) bool {
	for _, t := range tokens {
		for _, w := range words {
			if t == w {
				return true
			}
		}
	}
	return false
}

func hasStem(s string, stems ...string) bool {
	for _, st := range stems {
		if strings.Contains(s, st) {
			return true
		}
	}
	return false
}

func res(category, lang string, reply bool, reason string) Result {
	return Result{Category: category, Lang: lang, Reply: reply, Reason: reason}
}

func detectLang(norm string, tokens []string, letters, cyr int) string {
	if letters == 0 {
		return "uz"
	}
	if cyr*2 > letters {
		if strings.ContainsAny(norm, "ўқғҳ") || hasStem(norm, uzCyrStems...) {
			return "uz"
		}
		return "ru"
	}
	en, uz := 0, 0
	for _, t := range tokens {
		if enWords[t] {
			en++
		}
		if uzWords[t] {
			uz++
		}
	}
	if strings.Contains(norm, "o'") || strings.Contains(norm, "g'") {
		uz++
	}
	if en > uz {
		return "en"
	}
	return "uz"
}

func isSpam(norm string) bool {
	score := 0
	if reURL.MatchString(norm) {
		score += 2
	}
	if rePhone.MatchString(norm) {
		score += 2
	}
	for _, st := range promoStems {
		if strings.Contains(norm, st) {
			score++
		}
	}
	return score >= 2
}

func isQuestion(norm string, tokens []string) bool {
	if strings.ContainsAny(norm, "?？") {
		return true
	}
	for _, t := range tokens {
		if questionAnywhere[t] {
			return true
		}
	}
	if len(tokens) == 0 || strings.Contains(norm, "!") {
		return false
	}
	return questionFirst[tokens[0]]
}

// Classify kommentni qoidalar asosida tasniflaydi. Shubhali holatda xavfsiz (neytral) kategoriyani tanlaydi.
func Classify(text string) Result {
	norm := normalize(text)
	letters, _, cyr, symbols := analyze(norm)
	tokens := tokenize(norm)
	lang := detectLang(norm, tokens, letters, cyr)

	if letters == 0 {
		if symbols > 0 {
			return res("emoji", lang, true, "")
		}
		return res("empty", lang, false, "bo'sh yoki tushunarsiz komment")
	}

	stripped := strings.TrimSpace(reMention.ReplaceAllString(norm, ""))
	if sl, _, _, _ := analyze(stripped); sl == 0 {
		return res("special", lang, false, "faqat boshqalarni belgilash")
	}

	if isSpam(norm) {
		return res("spam", lang, false, "spam yoki reklama")
	}
	if hasStem(norm, abuseStems...) {
		return res("abuse", lang, false, "haqorat yoki provokatsiya")
	}
	if hasWord(tokens, criticismWords...) || hasStem(norm, criticismStems...) {
		return res("criticism", lang, true, "")
	}

	hasQ := strings.ContainsAny(norm, "?？")
	if len(tokens) <= 6 && !hasQ && hasStem(norm, islamicGreetStems...) {
		return res("greeting_islamic", lang, true, "")
	}
	if hasStem(norm, mashStems...) && !hasStem(norm, "asrasin", "omin", "ilohim", "baraka", "барак") {
		return res("praise", lang, true, "")
	}
	if len(tokens) <= 6 && !hasQ && (hasWord(tokens, greetWords...) || hasStem(norm, greetStems...)) {
		return res("greeting", lang, true, "")
	}
	if len(tokens) <= 12 && (hasWord(tokens, duaWords...) || hasStem(norm, duaStems...)) {
		return res("dua", lang, true, "")
	}
	if isQuestion(norm, tokens) {
		return res("question", lang, true, "")
	}
	if len(tokens) >= 12 {
		return res("long", lang, true, "")
	}
	if hasWord(tokens, praiseWords...) || hasStem(norm, praiseStems...) {
		return res("praise", lang, true, "")
	}
	return res("other", lang, true, "")
}
