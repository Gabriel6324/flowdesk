package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const RetrievalVersion = "keyword-bigram-v1"

var stop = map[string]bool{"the": true, "and": true, "for": true, "this": true, "that": true, "with": true, "please": true, "我想": true, "希望": true, "帮忙": true, "如何": true, "请问": true, "今天": true, "一个": true, "我的": true}

func Tokens(s string) map[string]bool {
	out := map[string]bool{}
	s = strings.ToLower(s)
	word := []rune{}
	chinese := []rune{}
	flushWord := func() {
		if len(word) >= 2 && !stop[string(word)] {
			out[string(word)] = true
		}
		word = nil
	}
	flushChinese := func() {
		for i := 1; i < len(chinese); i++ {
			t := string(chinese[i-1 : i+1])
			if !stop[t] {
				out[t] = true
			}
		}
		chinese = nil
	}
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			flushWord()
			chinese = append(chinese, r)
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			flushChinese()
			word = append(word, r)
		} else {
			flushWord()
			flushChinese()
		}
	}
	flushWord()
	flushChinese()
	return out
}

type Retriever struct {
	Articles []domain.Article
	Sources  []domain.Source
}

func NewRetriever(a []domain.Article) *Retriever {
	return &Retriever{Articles: a, Sources: []domain.Source{}}
}
func excerpt(body string) string {
	r := []rune(body)
	if len(r) > 1200 {
		r = r[:1200]
	}
	return string(r)
}
func (r *Retriever) Search(query string, limit int) ([]domain.Source, bool) {
	type scored struct {
		a     domain.Article
		score int
	}
	q := Tokens(query)
	list := []scored{}
	for _, a := range r.Articles {
		if a.Archived {
			continue
		}
		title := Tokens(a.Title + " " + strings.Join(a.Tags, " "))
		body := Tokens(a.Body)
		score := 0
		for t := range q {
			if title[t] {
				score += 3
			}
			if body[t] {
				score++
			}
		}
		if score > 0 {
			list = append(list, scored{a, score})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		x, _ := repository.ParseID(list[i].a.ID)
		y, _ := repository.ParseID(list[j].a.ID)
		return x < y
	})
	found := []domain.Source{}
	exhausted := false
	for _, v := range list {
		if len(found) >= limit {
			break
		}
		var source *domain.Source
		for i := range r.Sources {
			if r.Sources[i].ArticleID == v.a.ID {
				source = &r.Sources[i]
				break
			}
		}
		if source == nil {
			if len(r.Sources) >= 5 {
				exhausted = true
				continue
			}
			ex := excerpt(v.a.Body)
			size := utf8.RuneCountInString(ex)
			for _, s := range r.Sources {
				size += utf8.RuneCountInString(s.Excerpt)
			}
			if size > 6000 {
				exhausted = true
				continue
			}
			h := sha256.Sum256([]byte(v.a.Body))
			r.Sources = append(r.Sources, domain.Source{ArticleID: v.a.ID, EvidenceID: "K" + v.a.ID, Version: v.a.Version, Title: v.a.Title, Excerpt: ex, BodyHash: hex.EncodeToString(h[:])})
			source = &r.Sources[len(r.Sources)-1]
		}
		found = append(found, *source)
	}
	return found, exhausted
}
