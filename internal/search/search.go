// Package search ranks skills lexically.
//
// Tiers and scores: exact name 100, name prefix 80, name tokens 60,
// description 40, body 20. Every query token must match a token of the field
// that wins the tier. Two tokens match when they are equal, when the field
// token extends the query, or when the query extends the field token by at most
// two characters. Both tokens need at least four characters. The prefix rules
// let "hyperframe" find "hyperframes", "hyperframes" find "hyperframe", and
// "access" find "accessibility".
//
// FindPartial is an opt-in fallback for a query that no record matches in
// full: it ranks records that match at least one query token, with scores
// below the body tier.
package search

import (
	"sort"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/library"
)

// Scores of the five tiers.
const (
	ScoreExact       = 100
	ScorePrefix      = 80
	ScoreNameTokens  = 60
	ScoreDescription = 40
	ScoreBody        = 20
)

// Hit is a ranked record.
type Hit struct {
	Record *library.Record
	Score  int
}

// Tokenize lowercases s and splits it on runs of characters outside [a-z0-9].
func Tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}

// Find ranks recs for the query and returns at most limit hits. A limit below 1
// means no limit. Ties break own pack first, then trust order, then name.
func Find(recs []*library.Record, query string, limit int) []Hit {
	return find(recs, query, limit, nil)
}

// stats counts the work of one search. Tests use it to check the body skip.
type stats struct{ bodyScans int }

func find(recs []*library.Record, query string, limit int, st *stats) []Hit {
	query = strings.TrimSpace(query)
	qtok := Tokenize(query)

	// Tiers 1 to 4 need only the name and description.
	var hits []Hit
	var rest []*library.Record
	for _, r := range recs {
		if s := scoreHead(r, query, qtok); s > 0 {
			hits = append(hits, Hit{r, s})
		} else {
			rest = append(rest, r)
		}
	}
	// Body matching is the slow tier; skip it when the better tiers already
	// fill the limit, because body hits would sort after them and be cut.
	if len(qtok) > 0 && (limit < 1 || len(hits) < limit) {
		for _, r := range rest {
			if st != nil {
				st.bodyScans++
			}
			if containsAll(Tokenize(r.Body), qtok) {
				hits = append(hits, Hit{r, ScoreBody})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return library.Less(hits[i].Record, hits[j].Record)
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func scoreHead(r *library.Record, query string, qtok []string) int {
	if query != "" && (strings.EqualFold(query, r.Name) || strings.EqualFold(query, r.ID)) {
		return ScoreExact
	}
	if len(qtok) == 0 {
		return 0
	}
	name := Tokenize(r.Name)
	if matchesHead(name, qtok) {
		return ScorePrefix
	}
	if containsAll(name, qtok) {
		return ScoreNameTokens
	}
	if containsAll(Tokenize(r.Description), qtok) {
		return ScoreDescription
	}
	return 0
}

// matchesHead reports whether the query tokens match the leading name tokens.
func matchesHead(name, want []string) bool {
	if len(want) > len(name) {
		return false
	}
	for i, w := range want {
		if !sameWord(name[i], w) {
			return false
		}
	}
	return true
}

// sameWord reports whether a field token and a query token name the same word.
// Short tokens must match exactly. A field token may extend the query, so
// "access" matches "accessibility". A query may extend the field token only by
// a short suffix of at most two characters: a plural query still finds a
// singular skill ("hyperframes" finds "hyperframe"), but "database" does not
// match the broader token "data".
func sameWord(field, query string) bool {
	if field == query {
		return true
	}
	if len(field) < 4 || len(query) < 4 {
		return false
	}
	if strings.HasPrefix(field, query) {
		return true
	}
	return len(query)-len(field) <= 2 && strings.HasPrefix(query, field)
}

// containsAll reports whether every want token matches a token in have.
func containsAll(have, want []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if sameWord(h, w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Partial scores. A partial hit only appears when no record matches every
// query token in one field, so partial scores stay below the body tier.
const (
	partialName        = 3
	partialDescription = 2
	partialBody        = 1
	// partialAll is the score of a record that matches every token, but
	// not in one field.
	partialAll = ScoreBody - 1
	// partialMax caps the score of a record that misses a token.
	partialMax = partialAll - 1
	// partialMinToken is the shortest token that partial search uses, so
	// words such as "to" and "a" add no noise.
	partialMinToken = 3
)

// FindPartial ranks the records that match at least one query token. It
// ignores repeated tokens and tokens shorter than three characters. A record
// that matches every token, in any mix of fields, scores 19. For other
// records, each matched token adds 3 for a name match, 2 for a description
// match, or 1 for a body match; the best field counts and the score is at
// most 18. Callers use it as a fallback when Find returns no hits.
func FindPartial(recs []*library.Record, query string, limit int) []Hit {
	var qtok []string
	seen := map[string]bool{}
	for _, q := range Tokenize(strings.TrimSpace(query)) {
		if len(q) >= partialMinToken && !seen[q] {
			seen[q] = true
			qtok = append(qtok, q)
		}
	}
	if len(qtok) == 0 {
		return nil
	}
	var hits []Hit
	for _, r := range recs {
		name, desc := Tokenize(r.Name), Tokenize(r.Description)
		var body []string
		score, matched := 0, 0
		for _, q := range qtok {
			w := 0
			switch {
			case containsAll(name, []string{q}):
				w = partialName
			case containsAll(desc, []string{q}):
				w = partialDescription
			default:
				if body == nil {
					body = Tokenize(r.Body)
				}
				if containsAll(body, []string{q}) {
					w = partialBody
				}
			}
			if w > 0 {
				score += w
				matched++
			}
		}
		switch {
		case matched == 0:
			continue
		case matched == len(qtok):
			score = partialAll
		default:
			score = min(score, partialMax)
		}
		hits = append(hits, Hit{r, score})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return library.Less(hits[i].Record, hits[j].Record)
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
