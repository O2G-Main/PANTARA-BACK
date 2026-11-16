package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/domain/engine"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
)

const (
	PromptVersion = "taskdna-v1"
	SamplesCount  = 5
	CacheTTL      = 24 * time.Hour
)

// HybridTaskDNAnalyzer uses a 5-sample LLM endpoint when configured, deduplicated with
// singleflight and SHA-256 cached, and always falls back safely to deterministic analysis.
type HybridTaskDNAnalyzer struct {
	providerURL string
	apiKey      string
	model       string
	timeout     time.Duration
	cache       port.SecurityStore
	client      *http.Client
	sf          singleflight.Group
}

func NewHybridTaskDNAnalyzer(providerURL, apiKey, model string, timeout time.Duration, cache port.SecurityStore) *HybridTaskDNAnalyzer {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &HybridTaskDNAnalyzer{
		providerURL: strings.TrimRight(providerURL, "/"),
		apiKey:      apiKey,
		model:       model,
		timeout:     timeout,
		cache:       cache,
		client:      &http.Client{Timeout: timeout},
	}
}

func computeInputHash(title, description string, priority task.Priority, catalog []member.Competency) string {
	h := sha256.New()
	h.Write([]byte(PromptVersion))
	h.Write([]byte{0})
	h.Write([]byte(title))
	h.Write([]byte{0})
	h.Write([]byte(description))
	h.Write([]byte{0})
	h.Write([]byte(priority))
	ids := make([]string, len(catalog))
	for i, c := range catalog {
		ids[i] = c.ID
	}
	sort.Strings(ids)
	for _, id := range ids {
		h.Write([]byte{0})
		h.Write([]byte(id))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Analyze produces structured TaskDNA, never failing if external AI is disabled or unreachable.
func (a *HybridTaskDNAnalyzer) Analyze(
	ctx context.Context,
	title, description string,
	priority task.Priority,
	catalog []member.Competency,
	tasks []task.Task,
) (task.TaskDNA, error) {
	hash := computeInputHash(title, description, priority, catalog)
	if a.cache != nil {
		if cached, hit, _ := a.cache.GetCachedDNA(ctx, hash); hit && cached != nil {
			cp := *cached
			cp.InputHash = hash
			return cp, nil
		}
	}

	val, err, _ := a.sf.Do(hash, func() (any, error) {
		var dna task.TaskDNA
		if a.providerURL != "" && a.apiKey != "" {
			if llmDNA, llmErr := a.analyzeViaLLM(ctx, title, description, catalog); llmErr == nil {
				dna = llmDNA
			} else {
				dna = engine.AnalyzeDNADeterministic(title, description, priority, catalog)
			}
		} else {
			dna = engine.AnalyzeDNADeterministic(title, description, priority, catalog)
		}
		dna.InputHash = hash
		dna.Prompt = PromptVersion
		if a.cache != nil {
			_ = a.cache.SetCachedDNA(context.Background(), hash, dna, CacheTTL)
		}
		return dna, nil
	})
	if err != nil {
		fallback := engine.AnalyzeDNADeterministic(title, description, priority, catalog)
		fallback.InputHash = hash
		return fallback, nil
	}
	return val.(task.TaskDNA), nil
}

type llmSampleResponse struct {
	Requirements []struct {
		CompetencyID string `json:"competencyId"`
		Level        int    `json:"level"`
		Quote        string `json:"quote"`
	} `json:"requirements"`
	EffortSize string `json:"effortSize"`
	Complexity int    `json:"complexity"`
}

func (a *HybridTaskDNAnalyzer) analyzeViaLLM(
	ctx context.Context,
	title, description string,
	catalog []member.Competency,
) (task.TaskDNA, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	var catalogDesc strings.Builder
	allowed := make(map[string]struct{}, len(catalog))
	for _, c := range catalog {
		allowed[c.ID] = struct{}{}
		catalogDesc.WriteString(fmt.Sprintf("- %s (%s)\n", c.ID, c.Name))
	}

	sysPrompt := "Ekstrak Task DNA dalam JSON valid tanpa markdown. Pilih competencyId HANYA dari katalog. Schema: {\"requirements\":[{\"competencyId\":string,\"level\":1|2|3,\"quote\":string}],\"effortSize\":\"S\"|\"M\"|\"L\"|\"XL\",\"complexity\":0|1|2|3}."
	userPrompt := fmt.Sprintf("Katalog:\n%s\nJudul: %s\nDeskripsi: %s", catalogDesc.String(), title, description)

	samples := make([]llmSampleResponse, SamplesCount)
	g, gCtx := errgroup.WithContext(ctx)
	for i := 0; i < SamplesCount; i++ {
		idx := i
		g.Go(func() error {
			res, err := a.callOneSample(gCtx, sysPrompt, userPrompt)
			if err != nil {
				return err
			}
			samples[idx] = res
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return task.TaskDNA{}, err
	}

	return aggregateSamples(samples, allowed, a.model), nil
}

func (a *HybridTaskDNAnalyzer) callOneSample(ctx context.Context, sysPrompt, userPrompt string) (llmSampleResponse, error) {
	body := map[string]any{
		"model":       a.model,
		"temperature": 0.7,
		"response_format": map[string]string{
			"type": "json_object",
		},
		"messages": []map[string]string{
			{"role": "system", "content": sysPrompt},
			{"role": "user", "content": userPrompt},
		},
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.providerURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return llmSampleResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return llmSampleResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return llmSampleResponse{}, fmt.Errorf("ai status %d", resp.StatusCode)
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return llmSampleResponse{}, err
	}
	if len(envelope.Choices) == 0 {
		return llmSampleResponse{}, fmt.Errorf("empty ai choices")
	}
	var parsed llmSampleResponse
	if err := json.Unmarshal([]byte(envelope.Choices[0].Message.Content), &parsed); err != nil {
		return llmSampleResponse{}, err
	}
	return parsed, nil
}

func aggregateSamples(samples []llmSampleResponse, allowed map[string]struct{}, model string) task.TaskDNA {
	type reqAgg struct {
		count int
		levels []int
		quote string
	}
	byID := make(map[string]*reqAgg)
	effortCounts := map[string]int{}
	minC, maxC, sumC := 3, 0, 0

	for _, s := range samples {
		seenInSample := map[string]bool{}
		for _, r := range s.Requirements {
			if _, ok := allowed[r.CompetencyID]; !ok || seenInSample[r.CompetencyID] {
				continue
			}
			seenInSample[r.CompetencyID] = true
			agg, exists := byID[r.CompetencyID]
			if !exists {
				agg = &reqAgg{}
				byID[r.CompetencyID] = agg
			}
			agg.count++
			lvl := r.Level
			if lvl < 1 {
				lvl = 1
			}
			if lvl > 3 {
				lvl = 3
			}
			agg.levels = append(agg.levels, lvl)
			if agg.quote == "" && r.Quote != "" {
				agg.quote = r.Quote
			}
		}
		effortCounts[s.EffortSize]++
		c := s.Complexity
		if c < 0 {
			c = 0
		}
		if c > 3 {
			c = 3
		}
		if c < minC {
			minC = c
		}
		if c > maxC {
			maxC = c
		}
		sumC += c
	}

	reqs := make([]task.Requirement, 0, len(byID))
	for id, agg := range byID {
		sort.Ints(agg.levels)
		medianLvl := member.Level(agg.levels[len(agg.levels)/2])
		reqs = append(reqs, task.Requirement{
			CompetencyID: id,
			Level:        medianLvl,
			P:            engine.Round2(float64(agg.count) / float64(len(samples))),
			Quote:        agg.quote,
		})
	}
	sort.SliceStable(reqs, func(i, j int) bool {
		if reqs[i].P != reqs[j].P {
			return reqs[i].P > reqs[j].P
		}
		return reqs[i].CompetencyID < reqs[j].CompetencyID
	})
	if len(reqs) > 5 {
		reqs = reqs[:5]
	}

	bestSize, bestCount := "M", 0
	for k, v := range effortCounts {
		if task.EffortSize(k).IsValid() && v > bestCount {
			bestSize = k
			bestCount = v
		}
	}
	hoursMap := map[string]float64{"S": 3, "M": 6, "L": 12, "XL": 20}
	return task.TaskDNA{
		Requirements:     reqs,
		EffortSize:       task.EffortSize(bestSize),
		EffortP:          engine.Round2(float64(bestCount) / float64(len(samples))),
		EffortHours:      hoursMap[bestSize],
		Complexity:       member.Level(sumC / len(samples)),
		ComplexitySpread: maxC - minC,
		Dependencies:     []task.Dependency{},
		Model:            model,
		Prompt:           PromptVersion,
		Confirmed:        false,
	}
}
