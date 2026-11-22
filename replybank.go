package main

import (
	_ "embed"
	"encoding/json"
	"math/rand"
	"os"
)

//go:embed replies.json
var embeddedReplies []byte

// ReplyBank tayyor javoblar banki: til -> kategoriya -> javoblar ro'yxati.
type ReplyBank struct {
	data map[string]map[string][]string
}

// loadReplyBank replies.json'ni (yoki REPLIES_FILE yo'lidagi faylni) o'qiydi.
func loadReplyBank(path string) (*ReplyBank, error) {
	raw := embeddedReplies
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	var m map[string]map[string][]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &ReplyBank{data: m}, nil
}

// Pick kategoriya bo'yicha javob tanlaydi, oxirgi ishlatilganlardan qochadi.
func (b *ReplyBank) Pick(lang, category string, recent []string) string {
	list := b.data[lang][category]
	if len(list) == 0 {
		list = b.data[lang]["thanks"]
	}
	if len(list) == 0 {
		list = b.data["uz"]["thanks"]
	}
	if len(list) == 0 {
		return "Rahmat 🤍"
	}
	used := map[string]bool{}
	for _, r := range recent {
		used[r] = true
	}
	var fresh []string
	for _, r := range list {
		if !used[r] {
			fresh = append(fresh, r)
		}
	}
	if len(fresh) == 0 {
		fresh = list
	}
	return fresh[rand.Intn(len(fresh))]
}
