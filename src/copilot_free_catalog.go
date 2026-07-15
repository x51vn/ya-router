package yarouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	htmlTableRowRE  = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	htmlTableCellRE = regexp.MustCompile(`(?is)<t[hd][^>]*>(.*?)</t[hd]>`)
	htmlTagRE       = regexp.MustCompile(`(?is)<[^>]+>`)
	parenTextRE     = regexp.MustCompile(`(?is)\([^)]*\)`)
	nonWordRE       = regexp.MustCompile(`[^a-z0-9]+`)
)

const (
	copilotSupportedModelsURL   = "https://docs.github.com/en/copilot/reference/ai-models/supported-models"
	copilotBillingModelsURL     = "https://docs.github.com/en/copilot/concepts/billing/copilot-requests"
	defaultFreeCatalogTTL       = 6 * time.Hour
	copilotFreeCatalogCacheFile = "copilot-free-model-catalog.json"
	copilotFreeCatalogUserAgent = "github-copilot-svcs/1.0"
)

type copilotFreeCatalogSnapshot struct {
	SourceURLs        []string  `json:"source_urls"`
	FetchedAt         time.Time `json:"fetched_at"`
	FreePlanModels    []string  `json:"free_plan_models"`
	ZeroPremiumModels []string  `json:"zero_premium_models"`
	EligibleModels    []string  `json:"eligible_models"`
}

type CopilotFreeCatalog struct {
	mu        sync.RWMutex
	snapshot  *copilotFreeCatalogSnapshot
	ttl       time.Duration
	cachePath string
	client    *http.Client
}

type htmlTableCell struct {
	Raw  string
	Text string
}

func NewCopilotFreeCatalog(ttl time.Duration) *CopilotFreeCatalog {
	if ttl <= 0 {
		ttl = defaultFreeCatalogTTL
	}
	cachePath, err := getRuntimeStatePath(copilotFreeCatalogCacheFile)
	if err != nil {
		log.Printf("copilot free catalog: state path unavailable: %v", err)
	}
	c := &CopilotFreeCatalog{
		ttl:       ttl,
		cachePath: cachePath,
	}
	_ = c.loadCache()
	return c
}

func (c *CopilotFreeCatalog) EligibleModels(ctx context.Context) ([]string, error) {
	snap, err := c.snapshotForUse(ctx)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), snap.EligibleModels...), nil
}

func (c *CopilotFreeCatalog) snapshotForUse(ctx context.Context) (*copilotFreeCatalogSnapshot, error) {
	c.mu.RLock()
	snap := c.snapshot
	fresh := c.isFresh(snap)
	c.mu.RUnlock()
	if fresh {
		return cloneCatalogSnapshot(snap), nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isFresh(c.snapshot) {
		return cloneCatalogSnapshot(c.snapshot), nil
	}

	if err := c.refreshLocked(ctx); err != nil {
		if c.snapshot != nil {
			log.Printf("copilot free catalog: refresh failed, using cached snapshot: %v", err)
			return cloneCatalogSnapshot(c.snapshot), nil
		}
		return nil, err
	}
	return cloneCatalogSnapshot(c.snapshot), nil
}

func (c *CopilotFreeCatalog) refreshLocked(ctx context.Context) error {
	supportedHTML, err := c.fetchURL(ctx, copilotSupportedModelsURL)
	if err != nil {
		return fmt.Errorf("fetch supported models: %w", err)
	}
	billingHTML, err := c.fetchURL(ctx, copilotBillingModelsURL)
	if err != nil {
		return fmt.Errorf("fetch billing models: %w", err)
	}

	freePlanModels, err := parseCopilotPlanFreeModels(supportedHTML)
	if err != nil {
		return fmt.Errorf("parse free-plan models: %w", err)
	}
	zeroPremiumModels, err := parseZeroPremiumModels(billingHTML)
	if err != nil {
		return fmt.Errorf("parse zero-premium models: %w", err)
	}

	c.snapshot = &copilotFreeCatalogSnapshot{
		SourceURLs: []string{
			copilotSupportedModelsURL,
			copilotBillingModelsURL,
		},
		FetchedAt:         time.Now().UTC(),
		FreePlanModels:    freePlanModels,
		ZeroPremiumModels: zeroPremiumModels,
		EligibleModels:    intersectNormalizedModelNames(freePlanModels, zeroPremiumModels),
	}
	if err := c.saveCache(); err != nil {
		log.Printf("copilot free catalog: save cache failed: %v", err)
	}
	return nil
}

func (c *CopilotFreeCatalog) fetchURL(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", copilotFreeCatalogUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *CopilotFreeCatalog) httpClient() *http.Client {
	if c.client != nil {
		return c.client
	}
	if sharedHTTPClient != nil {
		return sharedHTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *CopilotFreeCatalog) isFresh(snapshot *copilotFreeCatalogSnapshot) bool {
	return snapshot != nil && time.Since(snapshot.FetchedAt) < c.ttl
}

func (c *CopilotFreeCatalog) loadCache() error {
	if c.cachePath == "" {
		return nil
	}
	body, err := os.ReadFile(c.cachePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var snapshot copilotFreeCatalogSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return err
	}
	c.mu.Lock()
	c.snapshot = &snapshot
	c.mu.Unlock()
	return nil
}

func (c *CopilotFreeCatalog) saveCache() error {
	if c.cachePath == "" || c.snapshot == nil {
		return nil
	}
	body, err := json.MarshalIndent(c.snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.cachePath, body, 0o600)
}

func cloneCatalogSnapshot(snapshot *copilotFreeCatalogSnapshot) *copilotFreeCatalogSnapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	clone.SourceURLs = append([]string(nil), snapshot.SourceURLs...)
	clone.FreePlanModels = append([]string(nil), snapshot.FreePlanModels...)
	clone.ZeroPremiumModels = append([]string(nil), snapshot.ZeroPremiumModels...)
	clone.EligibleModels = append([]string(nil), snapshot.EligibleModels...)
	return &clone
}

func getRuntimeStatePath(fileName string) (string, error) {
	configPath, err := getConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(configPath), fileName), nil
}

func parseCopilotPlanFreeModels(pageHTML string) ([]string, error) {
	rows, err := parseSectionTable(pageHTML, "supported-ai-models-per-copilot-plan")
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("copilot plan table is empty")
	}

	freeCol := -1
	for i, cell := range rows[0] {
		if normalizeModelName(cell.Text) == "copilot free" {
			freeCol = i
			break
		}
	}
	if freeCol == -1 {
		return nil, errors.New("copilot free column not found")
	}

	var models []string
	for _, row := range rows[1:] {
		if len(row) <= freeCol {
			continue
		}
		modelName := normalizeModelName(row[0].Text)
		if modelName == "" {
			continue
		}
		if cellHasIncludedMarker(row[freeCol].Raw) {
			models = append(models, modelName)
		}
	}
	return uniqueNormalized(models), nil
}

func parseZeroPremiumModels(pageHTML string) ([]string, error) {
	rows, err := parseSectionTable(pageHTML, "model-multipliers")
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("model multipliers table is empty")
	}

	paidCol := -1
	for i, cell := range rows[0] {
		if strings.Contains(normalizeModelName(cell.Text), "paid plans") {
			paidCol = i
			break
		}
	}
	if paidCol == -1 {
		return nil, errors.New("paid plans column not found")
	}

	var models []string
	for _, row := range rows[1:] {
		if len(row) <= paidCol {
			continue
		}
		modelName := normalizeModelName(row[0].Text)
		if modelName == "" {
			continue
		}
		if normalizeCellText(row[paidCol].Text) == "0" {
			models = append(models, modelName)
		}
	}
	return uniqueNormalized(models), nil
}

func intersectNormalizedModelNames(left, right []string) []string {
	allowed := make(map[string]bool, len(right))
	for _, item := range right {
		allowed[item] = true
	}

	var out []string
	seen := make(map[string]bool)
	for _, item := range left {
		if allowed[item] && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func resolveEffectiveCopilotFreeModels(docEligible []string, upstream *ModelList) []Model {
	if upstream == nil {
		return nil
	}

	var resolved []Model
	used := make(map[string]bool)

	for _, target := range docEligible {
		bestIdx := -1
		bestScore := -1
		for i, model := range upstream.Data {
			if used[model.ID] {
				continue
			}
			score := scoreModelCandidate(target, model)
			if score > bestScore {
				bestIdx = i
				bestScore = score
			}
		}
		if bestIdx >= 0 && bestScore >= 0 {
			model := upstream.Data[bestIdx]
			resolved = append(resolved, model)
			used[model.ID] = true
		}
	}

	return resolved
}

func scoreModelCandidate(target string, model Model) int {
	score := -1
	for _, candidate := range modelCandidateKeys(model) {
		switch {
		case candidate == target:
			score = max(score, 100)
		case strings.HasPrefix(candidate, target+" "):
			score = max(score, 80)
		}
	}
	if score < 0 {
		return score
	}
	if model.ModelPickerEnabled {
		score += 20
	}
	if !model.Preview {
		score += 5
	}
	return score
}

func modelCandidateKeys(model Model) []string {
	candidates := []string{
		normalizeModelName(model.Name),
		normalizeModelName(model.ID),
		normalizeModelName(model.Version),
	}

	var keys []string
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		for _, key := range []string{candidate, trimTrailingVersionTokens(candidate)} {
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

func trimTrailingVersionTokens(name string) string {
	tokens := strings.Fields(name)
	for len(tokens) > 0 && isAllDigits(tokens[len(tokens)-1]) {
		tokens = tokens[:len(tokens)-1]
	}
	return strings.Join(tokens, " ")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseSectionTable(pageHTML, sectionID string) ([][]htmlTableCell, error) {
	tableHTML, err := extractTableForSection(pageHTML, sectionID)
	if err != nil {
		return nil, err
	}

	matches := htmlTableRowRE.FindAllStringSubmatch(tableHTML, -1)
	rows := make([][]htmlTableCell, 0, len(matches))
	for _, match := range matches {
		var row []htmlTableCell
		for _, cellMatch := range htmlTableCellRE.FindAllStringSubmatch(match[1], -1) {
			raw := cellMatch[1]
			row = append(row, htmlTableCell{
				Raw:  raw,
				Text: normalizeCellText(stripHTML(raw)),
			})
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("no rows found in section table")
	}
	return rows, nil
}

func extractTableForSection(pageHTML, sectionID string) (string, error) {
	needle := `id="` + strings.ToLower(sectionID) + `"`
	htmlLower := strings.ToLower(pageHTML)
	sectionStart := strings.Index(htmlLower, needle)
	if sectionStart == -1 {
		return "", errors.New("section not found: " + sectionID)
	}
	tableStartRel := strings.Index(htmlLower[sectionStart:], "<table")
	if tableStartRel == -1 {
		return "", errors.New("table not found for section: " + sectionID)
	}
	tableStart := sectionStart + tableStartRel
	tableEndRel := strings.Index(htmlLower[tableStart:], "</table>")
	if tableEndRel == -1 {
		return "", errors.New("table end not found for section: " + sectionID)
	}
	tableEnd := tableStart + tableEndRel + len("</table>")
	return pageHTML[tableStart:tableEnd], nil
}

func cellHasIncludedMarker(raw string) bool {
	rawLower := strings.ToLower(raw)
	return strings.Contains(rawLower, `aria-label="included"`) ||
		strings.Contains(rawLower, `aria-label='included'`)
}

func stripHTML(s string) string {
	return html.UnescapeString(htmlTagRE.ReplaceAllString(s, " "))
}

func normalizeCellText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

func normalizeModelName(s string) string {
	s = strings.ToLower(html.UnescapeString(s))
	s = parenTextRE.ReplaceAllString(s, " ")
	s = nonWordRE.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func uniqueNormalized(items []string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func sortUniqueStrings(items []string) []string {
	items = uniqueNormalized(items)
	slices.Sort(items)
	return items
}
