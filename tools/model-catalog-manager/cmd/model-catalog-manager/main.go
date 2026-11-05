package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"sub2api-model-catalog-manager/internal/adminapi"
	"sub2api-model-catalog-manager/internal/app"
	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/config"
	"sub2api-model-catalog-manager/internal/inventorysummary"
	"sub2api-model-catalog-manager/internal/officialcatalog"
	"sub2api-model-catalog-manager/internal/report"
	"sub2api-model-catalog-manager/internal/securecred"
)

const maxPlanBytes = 4 << 20

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: model-catalog-manager <inspect|adopt|discover|apply|run|official-review|inventory-summary> [options]")
	}
	switch args[0] {
	case "inspect":
		return runInspect(args[1:])
	case "adopt":
		return runAdopt(args[1:])
	case "discover":
		return runDiscover(args[1:])
	case "apply":
		return runApply(args[1:])
	case "run":
		return runAutomatic(args[1:])
	case "official-review":
		return runOfficialReview(args[1:])
	case "inventory-summary":
		return runInventorySummary(args[1:])
	default:
		return errors.New("unknown command; use inspect, adopt, discover, apply, run, official-review, or inventory-summary")
	}
}

func runInventorySummary(args []string) error {
	fs := flag.NewFlagSet("inventory-summary", flag.ContinueOnError)
	inspectPath := fs.String("inspect", "", "complete all-account inspect JSON report")
	discoverPath := fs.String("discover", "", "complete all-account discover JSON report")
	outDir := fs.String("out-dir", "", "local directory for the summary report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inspectPath == "" || *discoverPath == "" || *outDir == "" || fs.NArg() != 0 {
		return errors.New("inventory-summary requires --inspect <inspect.json>, --discover <discover.json>, and --out-dir <directory>")
	}
	inspect, err := readInventory(*inspectPath)
	if err != nil {
		return errors.New("inventory-summary inspect input is invalid or unavailable")
	}
	discover, err := readInventory(*discoverPath)
	if err != nil {
		return errors.New("inventory-summary discover input is invalid or unavailable")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return errors.New("inventory-summary report ID could not be created")
	}
	report, err := inventorysummary.Build(inspect, discover, time.Now().UTC(), hex.EncodeToString(id[:]))
	if err != nil {
		return err
	}
	path, err := inventorysummary.Write(*outDir, report)
	if err != nil {
		return err
	}
	fmt.Printf("inventory summary complete: accounts=%d public_id_routes=%d upstream_id_reuse=%d discovered_id_reuse=%d conflicts=%d catalog_coverage=%s report=%s\n",
		report.Counts.Accounts, len(report.PublicIDRoutes), len(report.UpstreamIDReuse), len(report.DiscoveredIDReuse), len(report.IntraAccountMappingConflicts), report.CatalogCoverage, path)
	return nil
}

func readInventory(path string) (officialcatalog.Inventory, error) {
	var inventory officialcatalog.Inventory
	f, err := os.Open(path)
	if err != nil {
		return inventory, errors.New("input file unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > officialcatalog.MaxInputReportBytes {
		return inventory, errors.New("input must be a small regular JSON file")
	}
	return officialcatalog.ParseInventory(f)
}

func runOfficialReview(args []string) error {
	return runOfficialReviewWith(args, os.LookupEnv, officialcatalog.NewHTTPClient())
}

func runOfficialReviewWith(args []string, lookupEnv func(string) (string, bool), doer officialcatalog.HTTPDoer) error {
	fs := flag.NewFlagSet("official-review", flag.ContinueOnError)
	inputPath := fs.String("input", "", "prior inspect or discover JSON report")
	outDir := fs.String("out-dir", "", "local directory for the read-only review report")
	aliyunRegion := fs.String("aliyun-region", "", "Alibaba region enum: beijing, singapore, hongkong, frankfurt, tokyo, virginia")
	aliyunWorkspace := fs.String("aliyun-workspace-id", "", "Alibaba workspace ID only for beijing, frankfurt, tokyo, or virginia")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inputPath == "" || *outDir == "" || fs.NArg() != 0 {
		return errors.New("official-review requires --input <inspect-or-discover.json> and --out-dir <directory>")
	}
	if lookupEnv == nil || doer == nil {
		return errors.New("official-review dependencies are unavailable")
	}
	f, err := os.Open(*inputPath)
	if err != nil {
		return errors.New("official-review input report could not be opened")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > officialcatalog.MaxInputReportBytes {
		return errors.New("official-review input must be a small regular JSON file")
	}
	inventory, err := officialcatalog.ParseInventory(f)
	if err != nil {
		return err
	}
	keys := map[string][]byte{}
	for provider, envName := range map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"deepseek":  "DEEPSEEK_API_KEY",
		"alibaba":   "DASHSCOPE_API_KEY",
		"xai":       "XAI_API_KEY",
	} {
		value, ok := lookupEnv(envName)
		if ok && value != "" {
			keys[provider] = []byte(value)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	review, err := officialcatalog.Review(ctx, inventory, keys, officialcatalog.Options{AlibabaRegion: *aliyunRegion, AlibabaWorkspace: *aliyunWorkspace}, doer, time.Now)
	if err != nil {
		for _, key := range keys {
			zero(key)
		}
		return err
	}
	path, err := officialcatalog.Write(*outDir, review)
	if err != nil {
		return err
	}
	fmt.Printf("official review complete: observations=%d candidates=%d report=%s\n", len(review.Observations), len(review.NameShape), path)
	return nil
}

func runInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to local JSON configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || fs.NArg() != 0 {
		return errors.New("inspect requires --config <path>")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	doc, err := (app.Inspector{ReadCredential: securecred.Read, NewClient: makeClient}).Inspect(ctx, cfg)
	if err != nil {
		return err
	}
	path, err := report.Write(cfg.ReportDir, doc)
	if err != nil {
		return err
	}
	fmt.Printf("inspection complete: accounts=%d report=%s\n", len(doc.Accounts), path)
	return nil
}

func runAdopt(args []string) error {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to local JSON configuration")
	accountID := fs.Int64("account-id", 0, "allowlisted account ID")
	keysFile := fs.String("keys-file", "", "JSON string array of exact model_mapping keys")
	expectedHash := fs.String("expected-mapping-hash", "", "SHA256 of the complete current mapping")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *keysFile == "" || *expectedHash == "" || *accountID <= 0 || fs.NArg() != 0 {
		return errors.New("adopt requires --config, --account-id, --keys-file, and --expected-mapping-hash")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := (app.Adopter{ReadCredential: securecred.Read, NewClient: makeClient}).Adopt(ctx, cfg, *accountID, *keysFile, *expectedHash); err != nil {
		return err
	}
	fmt.Printf("adoption complete: account=%d; local ownership state only; Sub2API was not written\n", *accountID)
	return nil
}

func runDiscover(args []string) error {
	return runDiscoverWith(args, securecred.Read, makeClient)
}

func runDiscoverWith(args []string, readCredential app.CredentialReader, newClient app.ClientFactory) error {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to local JSON configuration")
	manualSync := fs.Bool("manual-sync-authorized", false, "owner-authorized manual all-account sync; preview only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || fs.NArg() != 0 {
		return errors.New("discover requires --config <path>")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *manualSync && (!cfg.AllAccounts() || cfg.ApplyModeValue() != "preview") {
		return errors.New("--manual-sync-authorized requires account_scope=all and apply_mode=preview")
	}
	timeout := 90 * time.Second
	if *manualSync {
		timeout = 45 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result, err := (app.Discoverer{ReadCredential: readCredential, NewClient: newClient, ManualSyncAuthorized: *manualSync}).Discover(ctx, cfg)
	if err != nil {
		return err
	}
	reportPath, err := report.Write(cfg.ReportDir, result.Report)
	if err != nil {
		return err
	}
	if result.Plan == nil {
		fmt.Printf("discovery complete: no applicable plan; manual_native_sync_override=%t account_scan_status=%s accounts=%d report=%s\n", result.Report.ManualNativeSyncOverride, result.Report.AccountScanStatus, len(result.Report.Discovery), reportPath)
		return nil
	}
	fmt.Printf("discovery complete: manual_native_sync_override=%t accounts=%d report=%s plan=%s plan_sha256=%s\n", result.Report.ManualNativeSyncOverride, len(result.Plan.Accounts), reportPath, result.PlanPath, result.PlanSHA256)
	return nil
}

func runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to local JSON configuration")
	planPath := fs.String("plan", "", "local JSON plan file")
	confirmSHA := fs.String("confirm-plan-sha256", "", "exact SHA256 of the reviewed raw plan file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *planPath == "" || *confirmSHA == "" || fs.NArg() != 0 {
		return errors.New("apply requires --config <path> --plan <path> --confirm-plan-sha256 <sha256>")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	plan, digest, err := loadPlan(*planPath)
	if err != nil {
		return err
	}
	if digest != *confirmSHA {
		return errors.New("apply blocked: --confirm-plan-sha256 does not match the plan file raw bytes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := (app.Applier{ReadCredential: securecred.Read, NewClient: makeClient}).Apply(ctx, cfg, plan, digest, *confirmSHA, false)
	if err != nil {
		return fmt.Errorf("apply stopped (%s): %w", result.String(), err)
	}
	fmt.Printf("apply complete: %s\n", result.String())
	return nil
}

func runAutomatic(args []string) error {
	return runAutomaticWith(args, securecred.Read, makeClient)
}

func runAutomaticWith(args []string, readCredential app.CredentialReader, newClient app.ClientFactory) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to local JSON configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || fs.NArg() != 0 {
		return errors.New("run requires --config <path>")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	result, err := (app.Discoverer{ReadCredential: readCredential, NewClient: newClient, WaitForRefresh: true}).Discover(ctx, cfg)
	if err != nil {
		return err
	}
	reportPath, err := report.Write(cfg.ReportDir, result.Report)
	if err != nil {
		return err
	}
	if result.Plan == nil {
		fmt.Printf("run complete: preview only; no applicable plan; account_scan_status=%s accounts=%d report=%s\n", result.Report.AccountScanStatus, len(result.Report.Discovery), reportPath)
		return nil
	}
	_, rulesHash, err := catalog.LoadRulesWithHash(cfg.RulesFile)
	if err != nil {
		return err
	}
	if cfg.ApplyModeValue() != "approved_rules" || cfg.ApprovedRulesSHA256 == "" || cfg.ApprovedRulesSHA256 != rulesHash {
		fmt.Printf("run complete: preview only; approved rules hash is absent or mismatched; report=%s plan=%s plan_sha256=%s\n", reportPath, result.PlanPath, result.PlanSHA256)
		return nil
	}
	applyResult, err := (app.Applier{ReadCredential: readCredential, NewClient: newClient}).Apply(ctx, cfg, *result.Plan, result.PlanSHA256, "", true)
	if err != nil {
		return fmt.Errorf("run stopped (%s): %w", applyResult.String(), err)
	}
	fmt.Printf("run complete: %s report=%s plan=%s\n", applyResult.String(), reportPath, result.PlanPath)
	return nil
}

func makeClient(base string) (app.AdminAPI, error) { return adminapi.New(base) }

func loadPlan(path string) (catalog.ApprovalPlan, string, error) {
	var plan catalog.ApprovalPlan
	f, err := os.Open(path)
	if err != nil {
		return plan, "", errors.New("plan file is unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxPlanBytes {
		return plan, "", errors.New("plan file must be a small regular JSON file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPlanBytes+1))
	if err != nil || len(data) > maxPlanBytes {
		return plan, "", errors.New("plan file exceeds size limit")
	}
	defer zero(data)
	h := sha256.Sum256(data)
	digest := hex.EncodeToString(h[:])
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return plan, "", errors.New("plan file is invalid")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return plan, "", errors.New("plan file has trailing data")
	}
	if err := plan.ValidateShape(time.Now().UTC()); err != nil {
		return plan, "", errors.New("plan file failed structural or expiry validation")
	}
	return plan, digest, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
