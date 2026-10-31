package docindex

import (
	"strings"
	"unicode"
)

// cjkStopChars are single characters that carry no topic; a query piece holding
// one ("按公", "销按") is a grammar accident and only adds noise. Pieces are
// still indexed in full, only the query drops them.
const cjkStopChars = "的了是在把按和与及或并对将被让给从到为以其这那个请我你他她它吗呢吧啊"

const (
	maxQueryTerms = 40
	maxTermLen    = 40
)

func isHan(r rune) bool { return unicode.Is(unicode.Han, r) }

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// pieces splits text into the search units both indexing and querying use: a
// word of letters and digits stays whole, a run of Han characters becomes its
// overlapping two-character pieces (or the single character when the run has
// no neighbour). stop drops pieces made with a stop character.
func pieces(text string, stop bool) []string {
	var out []string
	var word []rune
	var han []rune
	flushWord := func() {
		if len(word) > 0 {
			if len(word) > maxTermLen {
				word = word[:maxTermLen]
			}
			out = append(out, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	flushHan := func() {
		switch {
		case len(han) == 1:
			if !stop || !strings.ContainsRune(cjkStopChars, han[0]) {
				out = append(out, string(han))
			}
		case len(han) > 1:
			for i := 0; i+1 < len(han); i++ {
				if stop && (strings.ContainsRune(cjkStopChars, han[i]) || strings.ContainsRune(cjkStopChars, han[i+1])) {
					continue
				}
				out = append(out, string(han[i:i+2]))
			}
		}
		han = han[:0]
	}
	for _, r := range text {
		switch {
		case isHan(r):
			flushWord()
			han = append(han, r)
		case isWordRune(r):
			flushHan()
			word = append(word, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return out
}

// Terms is the text stored in the full-text table for a chunk.
func Terms(text string) string { return strings.Join(pieces(text, false), " ") }

// MatchQuery turns a question into an FTS5 query: any of its pieces may match,
// and bm25 ranks the chunks that match most of them first. It returns "" when
// the question holds nothing searchable. Every piece is quoted, so user text can
// never be read as FTS5 syntax.
func MatchQuery(query string) string {
	var parts []string
	for _, p := range QueryTerms(query) {
		term := `"` + p + `"`
		// A longer Latin word also finds its inflections: invoice -> invoices.
		if prefixTerm(p) {
			term += "*"
		}
		parts = append(parts, term)
	}
	return strings.Join(parts, " OR ")
}

func prefixTerm(p string) bool {
	r := []rune(p)
	return len(r) >= 4 && !isHan(r[0])
}

// QueryTerms are the distinct search units MatchQuery looks for.
func QueryTerms(query string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range pieces(query, true) {
		if seen[p] {
			continue
		}
		r := []rune(p)
		// A lone letter or digit matches almost everything.
		if len(r) < 2 && !isHan(r[0]) {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) >= maxQueryTerms {
			break
		}
	}
	return out
}

// Coverage is the share of terms that occur in text, counted the way the query
// matches: a longer Latin term also matches its inflections. One shared word
// ("采购") can make a chunk a hit for a question about something else; Coverage
// says how much of the question the chunk really speaks to.
func Coverage(terms []string, text string) float64 {
	if len(terms) == 0 {
		return 0
	}
	have := map[string]bool{}
	var words []string
	for _, p := range pieces(text, false) {
		have[p] = true
		words = append(words, p)
	}
	hit := 0
	for _, t := range terms {
		if have[t] {
			hit++
			continue
		}
		if prefixTerm(t) {
			for _, w := range words {
				if strings.HasPrefix(w, t) {
					hit++
					break
				}
			}
		}
	}
	return float64(hit) / float64(len(terms))
}
