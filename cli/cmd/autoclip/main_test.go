package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChooseModelProfiles(t *testing.T) {
	manifest := defaultDependencyManifest()
	cases := map[string]string{
		"lite":      "base",
		"standard":  "small",
		"fast-high": "large-v3-turbo-q5_0",
		"best":      "large-v3",
	}
	for profile, want := range cases {
		got, err := chooseModel(manifest, profile, "", GlobalOptions{}, bytes.NewBuffer(nil), bytes.NewBuffer(nil))
		if err != nil {
			t.Fatalf("chooseModel(%s) returned error: %v", profile, err)
		}
		if got.Name != want {
			t.Fatalf("chooseModel(%s)=%s want %s", profile, got.Name, want)
		}
	}
	best, _ := chooseModel(manifest, "best", "", GlobalOptions{}, bytes.NewBuffer(nil), bytes.NewBuffer(nil))
	if !best.BestQuality {
		t.Fatal("best profile must be marked best_quality")
	}
}

func TestChooseModelRequiresExplicitProfileInNonInteractive(t *testing.T) {
	_, err := chooseModel(defaultDependencyManifest(), "", "", GlobalOptions{JSON: true}, bytes.NewBuffer(nil), bytes.NewBuffer(nil))
	if err == nil {
		t.Fatal("expected missing profile error")
	}
}

func TestFillerAndProtectSets(t *testing.T) {
	fillers := fillerSet("zh", "")
	if !fillers["嗯"] || !fillers["那个"] {
		t.Fatalf("expected default Chinese fillers, got %#v", fillers)
	}
	protected := protectSet("zh", "")
	if !protected["然后"] || !protected["所以"] {
		t.Fatalf("expected default Chinese protected words, got %#v", protected)
	}
	custom := fillerSet("zh", "嗯,呃")
	if !custom["嗯"] || custom["那个"] {
		t.Fatalf("custom filler list should replace defaults, got %#v", custom)
	}
}

func TestPlanCutsMergesAndKeepsSegments(t *testing.T) {
	p := ProcessOptions{Preset: "balanced"}
	candidates := []CutCandidate{
		removeCandidate("cut_00001", "filler", 1.0, 1.2, "嗯", 0.9, "test", "test", Preset{PrePadMS: 50, PostPadMS: 90}),
		removeCandidate("cut_00002", "silence", 1.30, 1.60, "", 1, "test", "test", Preset{PrePadMS: 50, PostPadMS: 90}),
		reviewCandidate("cut_00003", "filler", 3.0, 3.2, "然后", 0.9, "protected", Preset{}, true),
	}
	plan, err := planCuts(p, 10, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cuts) != 1 {
		t.Fatalf("expected merged single cut, got %d: %#v", len(plan.Cuts), plan.Cuts)
	}
	if len(plan.KeptSegments) != 2 {
		t.Fatalf("expected two kept segments, got %#v", plan.KeptSegments)
	}
	if plan.Cuts[0].Start >= plan.Cuts[0].End {
		t.Fatalf("invalid cut: %#v", plan.Cuts[0])
	}
}

func TestWordFillerDetectionRequiresSafeBoundary(t *testing.T) {
	p := ProcessOptions{Preset: "balanced", Language: "zh"}
	tr := Transcript{SchemaVersion: schemaVersion, Language: "zh", Segments: []TranscriptSeg{
		{ID: 0, Start: 0, End: 2.2, Text: "我 嗯 觉得", Words: []TranscriptWord{
			{Start: 0.1, End: 0.4, Text: "我", Confidence: 0.9},
			{Start: 0.65, End: 0.82, Text: "嗯", Confidence: 0.95},
			{Start: 1.1, End: 1.7, Text: "觉得", Confidence: 0.9},
		}},
		{ID: 1, Start: 3, End: 4, Text: "这个方案", Words: []TranscriptWord{
			{Start: 3.0, End: 3.2, Text: "这个", Confidence: 0.95},
			{Start: 3.21, End: 3.8, Text: "方案", Confidence: 0.95},
		}},
	}}
	candidates := detectCandidates(p, tr, MediaInfo{DurationSeconds: 10}, "")
	remove := 0
	review := 0
	for _, c := range candidates {
		if c.Decision == "remove" && c.Text == "嗯" {
			remove++
		}
		if c.Decision == "review" && c.Text == "这个" {
			review++
		}
	}
	if remove != 1 || review != 1 {
		t.Fatalf("expected safe remove and embedded review, got %#v", candidates)
	}
}

func TestEdgeSilenceCanTrimBoundaries(t *testing.T) {
	p := ProcessOptions{Preset: "balanced"}
	candidates := []CutCandidate{
		removeCandidate("cut_00001", "edge_silence", 0, 0.8, "", 1, "test", "leading", Preset{PrePadMS: 50, PostPadMS: 90}),
		removeCandidate("cut_00002", "edge_silence", 9.2, 10, "", 1, "test", "trailing", Preset{PrePadMS: 50, PostPadMS: 90}),
	}
	plan, err := planCuts(p, 10, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cuts) != 2 || plan.Cuts[0].Start != 0 || plan.Cuts[1].End != 10 {
		t.Fatalf("edge silence should survive edge guard, got %#v", plan.Cuts)
	}
}

func TestParseWhisperSegmentsReadsNestedOffsets(t *testing.T) {
	raw := []interface{}{
		map[string]interface{}{
			"text": "毕业设计快交了",
			"offsets": map[string]interface{}{
				"from": float64(1940),
				"to":   float64(4580),
			},
		},
		map[string]interface{}{
			"text": "我来帮你拆步骤",
			"timestamps": map[string]interface{}{
				"from": "00:00:04,580",
				"to":   "00:00:09,300",
			},
		},
	}
	segments := parseWhisperSegments(raw)
	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %#v", segments)
	}
	if segments[0].Start != 1.94 || segments[0].End != 4.58 {
		t.Fatalf("unexpected offset segment timing: %#v", segments[0])
	}
	if segments[1].Start != 4.58 || segments[1].End != 9.3 {
		t.Fatalf("unexpected timestamp segment timing: %#v", segments[1])
	}
}

func TestOutputPaths(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "中文 文件.mp4")
	if err := os.WriteFile(input, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := makeOutputPaths(input, "", true, "accurate")
	if filepath.Base(paths.Video) != "中文 文件.autoclip.mp4" {
		t.Fatalf("unexpected video path: %s", paths.Video)
	}
	audio := makeOutputPaths(input, "", true, "audio-only")
	if filepath.Ext(audio.Video) != ".m4a" {
		t.Fatalf("audio-only should output .m4a, got %s", audio.Video)
	}
	outDir := filepath.Join(dir, "out")
	outPaths := makeOutputPaths(input, outDir, true, "accurate")
	if filepath.Dir(outPaths.Video) != outDir {
		t.Fatalf("extensionless --out should be treated as directory, got %s", outPaths.Video)
	}
	if filepath.Base(paths.ReviewJSON) != "中文 文件.autoclip.transcript.review.json" {
		t.Fatalf("unexpected review path: %s", paths.ReviewJSON)
	}
	sidecars := makeTranscriptSidecarPaths(filepath.Join(dir, "clip.autoclip.transcript.json"), filepath.Join(dir, "fixed.corrected.json"))
	if filepath.Base(sidecars.CorrectedJSON) != "fixed.corrected.json" {
		t.Fatalf("unexpected corrected sidecar path: %s", sidecars.CorrectedJSON)
	}
}

func TestParseProcessFlagsAllowsFlagsAfterInput(t *testing.T) {
	var errOut bytes.Buffer
	p, code, ok := parseProcessFlags("render", []string{"input.mp4", "--preset", "aggressive", "--language", "zh", "--min-silence", "0.5", "--transcript", "fixed.json"}, GlobalOptions{}, &errOut)
	if !ok || code != exitOK {
		t.Fatalf("parseProcessFlags failed code=%d stderr=%s", code, errOut.String())
	}
	if p.Input != "input.mp4" || p.Preset != "aggressive" || p.Language != "zh" || p.MinSilence != 0.5 || p.Transcript != "fixed.json" {
		t.Fatalf("unexpected parsed options: %#v", p)
	}
}

func TestReorderInterspersedFlagsKeepsBoolFlags(t *testing.T) {
	got := reorderInterspersedFlags([]string{"input.mp4", "--dry-run", "--out", "outdir"}, map[string]bool{"dry-run": true})
	want := []string{"--dry-run", "--out", "outdir", "input.mp4"}
	if len(got) != len(want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v want %#v", got, want)
		}
	}
}

func TestReleaseManifestJSON(t *testing.T) {
	path := filepath.Join("..", "..", "..", "release", "dependency-manifest.json")
	var manifest DependencyManifest
	if err := readJSONFile(path, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Models) != 4 {
		t.Fatalf("expected 4 model profiles, got %d", len(manifest.Models))
	}
	encoded, err := json.Marshal(manifest)
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("manifest should roundtrip json: %v", err)
	}
	var best ModelEntry
	for _, m := range manifest.Models {
		if m.Profile == "best" {
			best = m
		}
		if m.Checksum == "" {
			t.Fatalf("model %s missing checksum", m.Name)
		}
		if len(m.MirrorURLs) == 0 {
			t.Fatalf("model %s missing mirror urls", m.Name)
		}
	}
	if best.Name != "large-v3" || !best.BestQuality {
		t.Fatalf("best profile must be large-v3 and best_quality, got %#v", best)
	}
}

func TestDownloadSourcesDedupes(t *testing.T) {
	got := downloadSources("https://example.test/model.bin", []string{"", "https://example.test/model.bin", "https://mirror.test/model.bin"})
	want := []string{"https://example.test/model.bin", "https://mirror.test/model.bin"}
	if len(got) != len(want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v want %#v", got, want)
		}
	}
}

func TestWriteASSAndPreview(t *testing.T) {
	dir := t.TempDir()
	tr := Transcript{SchemaVersion: schemaVersion, Language: "zh", Segments: []TranscriptSeg{{ID: 0, Start: 0, End: 1.2, Text: "你好 AutoClip"}}}
	ass := filepath.Join(dir, "out.ass")
	if err := writeASS(ass, tr, "large"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ass)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("Dialogue:")) || !bytes.Contains(data, []byte("你好 AutoClip")) {
		t.Fatalf("unexpected ass: %s", string(data))
	}
	plan := CutPlan{Preset: "balanced", Candidates: []CutCandidate{removeCandidate("c1", "filler", 1, 1.2, "嗯", 1, "test", "safe", Preset{})}}
	if err := writeCandidatePreviewHTML(filepath.Join(dir, "preview.html"), plan); err != nil {
		t.Fatal(err)
	}
}

func TestFilterGraphAudioEnhanceAndSubtitle(t *testing.T) {
	graph, maps := filterGraph([]TimeSegment{{Start: 0, End: 1}}, false, "loudnorm=I=-16:TP=-1.5:LRA=11", "subtitles='x.ass'")
	if len(maps) != 4 || !strings.Contains(graph, "loudnorm") || !strings.Contains(graph, "subtitles") {
		t.Fatalf("unexpected graph=%s maps=%#v", graph, maps)
	}
}

func TestTranscriptCorrectionPreservesTiming(t *testing.T) {
	original := Transcript{SchemaVersion: schemaVersion, Language: "zh", Segments: []TranscriptSeg{
		{ID: 0, Start: 1.2, End: 2.4, Text: "字幕出来后"},
		{ID: 1, Start: 2.4, End: 3.8, Text: "让AI过一边"},
	}}
	proposed := Transcript{SchemaVersion: schemaVersion, Language: "zh", Segments: []TranscriptSeg{
		{ID: 0, Start: 999, End: 1000, Text: "字幕出来后"},
		{ID: 1, Start: 999, End: 1000, Text: "让 AI 过一遍"},
	}}
	corrected, changes, err := applyTranscriptCorrection(original, proposed, "agent.json")
	if err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Fatalf("expected 1 change, got %d", changes)
	}
	if corrected.Segments[1].Text != "让 AI 过一遍" {
		t.Fatalf("unexpected corrected text: %#v", corrected.Segments[1])
	}
	if corrected.Segments[1].Start != 2.4 || corrected.Segments[1].End != 3.8 {
		t.Fatalf("timestamps should be preserved, got %#v", corrected.Segments[1])
	}
	if corrected.Metadata["corrected_by"] != "agent" {
		t.Fatalf("missing correction metadata: %#v", corrected.Metadata)
	}
}

func TestTranscriptCorrectionCleansZeroDurationAndDuplicates(t *testing.T) {
	original := Transcript{SchemaVersion: schemaVersion, Language: "zh", Segments: []TranscriptSeg{
		{ID: 0, Start: 0, End: 1, Text: "这个灵溪"},
		{ID: 1, Start: 1, End: 1, Text: "这个灵溪"},
		{ID: 2, Start: 1, End: 2, Text: "这个灵溪"},
		{ID: 3, Start: 2.5, End: 3, Text: "这个灵溪"},
	}}
	proposed := Transcript{SchemaVersion: schemaVersion, Language: "zh", Segments: []TranscriptSeg{
		{ID: 0, Text: "这个 WPS 灵犀"},
		{ID: 1, Text: "这个 WPS 灵犀"},
		{ID: 2, Text: "这个 WPS 灵犀"},
		{ID: 3, Text: "这个 WPS 灵犀"},
	}}
	corrected, changes, err := applyTranscriptCorrection(original, proposed, "agent.json")
	if err != nil {
		t.Fatal(err)
	}
	if changes != 4 {
		t.Fatalf("expected 4 text changes, got %d", changes)
	}
	if len(corrected.Segments) != 2 {
		t.Fatalf("expected zero-duration removal and adjacent duplicate merge, got %#v", corrected.Segments)
	}
	if corrected.Segments[0].Start != 0 || corrected.Segments[0].End != 2 {
		t.Fatalf("expected first duplicate group to span 0-2, got %#v", corrected.Segments[0])
	}
	if corrected.Metadata["cleanup_removed_segments"] != "1" || corrected.Metadata["cleanup_merged_duplicates"] != "1" {
		t.Fatalf("missing cleanup metadata: %#v", corrected.Metadata)
	}
}

func TestTranscriptReviewPackIncludesInstructionsAndIssues(t *testing.T) {
	tr := Transcript{SchemaVersion: schemaVersion, Language: "zh", Segments: []TranscriptSeg{
		{ID: 0, Start: 0, End: 1, Text: "这个字幕需要校准"},
		{ID: 1, Start: 1, End: 2, Text: "这个灵溪能帮咱村卖柿饼吗"},
	}}
	pack := buildTranscriptReviewPack("in.json", tr, "测试上下文", []string{"AutoClip"})
	if pack.Type != "agent_transcript_review" || len(pack.Instructions) == 0 {
		t.Fatalf("bad review pack: %#v", pack)
	}
	if len(pack.Issues) == 0 {
		t.Fatalf("expected confusion issues")
	}
	foundLingxi := false
	for _, issue := range pack.Issues {
		if strings.Contains(issue.Text, "灵溪") && strings.Contains(strings.Join(issue.Hints, ","), "灵犀") {
			foundLingxi = true
		}
	}
	if !foundLingxi {
		t.Fatalf("expected 灵溪 -> 灵犀 hint, got %#v", pack.Issues)
	}
}

func TestRunModelsListJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--json", "models", "list"}, &out, &errOut)
	if code != exitOK {
		t.Fatalf("run returned %d stderr=%s", code, errOut.String())
	}
	var models []ModelEntry
	if err := json.Unmarshal(out.Bytes(), &models); err != nil {
		t.Fatal(err)
	}
	if len(models) != 4 {
		t.Fatalf("expected 4 models, got %d", len(models))
	}
}
