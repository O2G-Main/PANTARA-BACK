package engine

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
)

// DefaultKeywords maps canonical competency IDs (aligned with seed & frontend mock-dna) to extraction keywords.
var DefaultKeywords = map[string][]string{
	"backend-api":       {"api", "endpoint", "rest", "route", "backend", "server"},
	"oauth2":            {"oauth", "sso", "login", "auth", "autentikasi", "token", "jwt", "sesi"},
	"integrasi-api":     {"integrasi", "webhook", "qris", "gateway", "eksternal", "sinkron", "kepegawaian"},
	"laravel":           {"laravel", "php", "artisan", "eloquent", "blade"},
	"arsitektur-data":   {"skema", "arsitektur", "relasi", "model data", "multi-tenant", "partisi"},
	"sql":               {"postgres", "sql", "query", "indeks", "database", "migrasi", "optimasi"},
	"react":             {"react", "next", "komponen", "halaman", "frontend", "formulir", "tabel", "tsx"},
	"tailwind":          {"tailwind", "css", "responsif", "mobile", "tampilan", "layout", "grid", "spacing"},
	"aksesibilitas":     {"aksesibilitas", "wcag", "kontras", "keyboard", "fokus", "aria", "screen reader"},
	"flutter":           {"flutter", "dart", "apk", "mobile app", "android", "ios"},
	"uiux":              {"ui", "ux", "alur", "interaksi", "wireframe", "onboarding"},
	"design-system":     {"design system", "komponen ui", "figma", "token", "katalog komponen"},
	"qa":                {"uji", "qa", "test", "regresi", "verifikasi", "skenario"},
	"e2e":               {"e2e", "playwright", "browser", "end-to-end"},
	"devops":            {"docker", "deploy", "ci", "cd", "pipeline", "container", "compose", "infra"},
	"technical-writing": {"dokumentasi", "openapi", "swagger", "panduan", "manual", "readme", "runbook"},
}

var (
	sentenceSplitRe = regexp.MustCompile(`[.!?]+\s*`)
	complexSignalRe = regexp.MustCompile(`aman|kompleks|migrasi|arsitektur|produksi`)
)

// AnalyzeDNADeterministic extracts structured TaskDNA deterministically from title, description,
// priority, and active organization competency catalog without external API dependencies.
func AnalyzeDNADeterministic(
	title, description string,
	priority task.Priority,
	catalog []member.Competency,
) task.TaskDNA {
	full := title + ". " + description
	lower := strings.ToLower(full)
	rawSentences := sentenceSplitRe.Split(description, -1)
	var sentences []string
	for _, s := range rawSentences {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" {
			sentences = append(sentences, trimmed)
		}
	}

	// Build unified lookup scoped to active catalog when available so we never emit unmapped IDs.
	keywordMap := make(map[string][]string)
	if len(catalog) > 0 {
		for _, c := range catalog {
			if kws, ok := DefaultKeywords[c.ID]; ok {
				keywordMap[c.ID] = kws
			} else {
				keywordMap[c.ID] = []string{strings.ToLower(c.Name), strings.ReplaceAll(c.ID, "-", " ")}
			}
		}
	} else {
		for id, kws := range DefaultKeywords {
			keywordMap[id] = kws
		}
	}

	reqs := make([]task.Requirement, 0)
	for id, kws := range keywordMap {
		var hits []string
		for _, kw := range kws {
			if strings.Contains(lower, kw) {
				hits = append(hits, kw)
			}
		}
		if len(hits) == 0 {
			continue
		}
		quote := strings.TrimRight(title, ".!?")
		for _, s := range sentences {
			sl := strings.ToLower(s)
			matched := false
			for _, h := range hits {
				if strings.Contains(sl, h) {
					matched = true
					break
				}
			}
			if matched {
				quote = strings.TrimRight(s, ".!?")
				break
			}
		}
		p := math.Min(0.97, Round2(0.58+0.13*float64(len(hits))))
		lvl := member.LevelBasic
		if complexSignalRe.MatchString(lower) {
			lvl = member.LevelIndep
		}
		reqs = append(reqs, task.Requirement{
			CompetencyID: id,
			Level:        lvl,
			P:            p,
			Quote:        quote,
		})
	}

	sort.SliceStable(reqs, func(i, j int) bool {
		if reqs[i].P != reqs[j].P {
			return reqs[i].P > reqs[j].P
		}
		return reqs[i].CompetencyID < reqs[j].CompetencyID
	})
	if len(reqs) > 4 {
		reqs = reqs[:4]
	}

	words := len(strings.Fields(full))
	effortSize := task.EffortL
	effortHours := 12.0
	effortP := 0.58
	if words < 25 {
		effortSize = task.EffortS
		effortHours = 3
		effortP = 0.72
	} else if words < 60 {
		effortSize = task.EffortM
		effortHours = 6
		effortP = 0.64
	}

	complexity := member.LevelBasic
	if priority == task.PriorityHigh {
		complexity = member.LevelIndep
	}

	return task.TaskDNA{
		Requirements:     reqs,
		EffortSize:       effortSize,
		EffortP:          effortP,
		EffortHours:      effortHours,
		Complexity:       complexity,
		ComplexitySpread: 1,
		Dependencies:     []task.Dependency{},
		Model:            "deterministic-rule-v1",
		Prompt:           "taskdna-v1",
		Confirmed:        false,
	}
}
