package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	exitOK = iota
	exitGeneralFailure
	exitInvalidInput
	exitUnsupportedMedia
	exitDependencyFailure
	exitTranscriptionFailure
	exitPlanningFailure
	exitRenderingFailure
	exitPermissionOrDisk
	exitCloudConfigRequired
)

const schemaVersion = "0.1"

type GlobalOptions struct {
	Config   string
	Home     string
	JSON     bool
	Progress string
	LogLevel string
	NoColor  bool
	Yes      bool
}

type DependencyManifest struct {
	SchemaVersion  string        `json:"schema_version"`
	LastVerifiedAt string        `json:"last_verified_at"`
	SourceNotes    []string      `json:"source_notes"`
	Models         []ModelEntry  `json:"models"`
	WhisperCPP     SourceTool    `json:"whispercpp"`
	Binaries       []BinaryEntry `json:"binaries,omitempty"`
}

type SourceTool struct {
	Version      string   `json:"version"`
	SourceURL    string   `json:"source_url"`
	MirrorURLs   []string `json:"mirror_urls,omitempty"`
	ChecksumType string   `json:"checksum_type"`
	Checksum     string   `json:"checksum"`
}

type ModelEntry struct {
	Profile      string   `json:"profile"`
	Name         string   `json:"name"`
	FileName     string   `json:"file_name"`
	URL          string   `json:"url"`
	MirrorURLs   []string `json:"mirror_urls,omitempty"`
	Size         string   `json:"size"`
	SizeBytes    int64    `json:"size_bytes"`
	ChecksumType string   `json:"checksum_type"`
	Checksum     string   `json:"checksum"`
	Quality      string   `json:"quality"`
	Speed        string   `json:"speed"`
	Use          string   `json:"use"`
	BestQuality  bool     `json:"best_quality"`
}

type BinaryEntry struct {
	Name          string   `json:"name"`
	OS            string   `json:"os"`
	Arch          string   `json:"arch"`
	URL           string   `json:"url"`
	MirrorURLs    []string `json:"mirror_urls,omitempty"`
	ArchiveType   string   `json:"archive_type"`
	PathInArchive string   `json:"path_in_archive"`
	ChecksumType  string   `json:"checksum_type"`
	Checksum      string   `json:"checksum"`
	Source        string   `json:"source"`
}

type InstallLock struct {
	SchemaVersion string                         `json:"schema_version"`
	UpdatedAt     string                         `json:"updated_at"`
	Home          string                         `json:"home"`
	Profile       string                         `json:"profile"`
	Model         InstalledDependency            `json:"model"`
	Tools         map[string]InstalledDependency `json:"tools"`
}

type InstalledDependency struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Version      string `json:"version,omitempty"`
	Source       string `json:"source,omitempty"`
	Size         string `json:"size,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	ChecksumType string `json:"checksum_type,omitempty"`
	Checksum     string `json:"checksum,omitempty"`
}

type MediaInfo struct {
	Path              string  `json:"path"`
	DurationSeconds   float64 `json:"duration_seconds"`
	VideoCodec        string  `json:"video_codec,omitempty"`
	AudioCodec        string  `json:"audio_codec,omitempty"`
	Width             int     `json:"width,omitempty"`
	Height            int     `json:"height,omitempty"`
	FrameRate         string  `json:"frame_rate,omitempty"`
	AudioSampleRate   int     `json:"audio_sample_rate,omitempty"`
	ProbeTool         string  `json:"probe_tool"`
	EstimatedSettings string  `json:"estimated_render_settings,omitempty"`
}

type Transcript struct {
	SchemaVersion string            `json:"schema_version"`
	Language      string            `json:"language"`
	Segments      []TranscriptSeg   `json:"segments"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type TranscriptSeg struct {
	ID    int              `json:"id"`
	Start float64          `json:"start"`
	End   float64          `json:"end"`
	Text  string           `json:"text"`
	Words []TranscriptWord `json:"words,omitempty"`
}

type TranscriptWord struct {
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence,omitempty"`
}

type TranscriptReviewPack struct {
	SchemaVersion    string            `json:"schema_version"`
	Type             string            `json:"type"`
	CreatedAt        string            `json:"created_at"`
	SourceTranscript string            `json:"source_transcript"`
	Language         string            `json:"language"`
	Context          string            `json:"context,omitempty"`
	Glossary         []string          `json:"glossary,omitempty"`
	Instructions     []string          `json:"instructions"`
	Issues           []TranscriptIssue `json:"issues,omitempty"`
	Segments         []TranscriptSeg   `json:"segments"`
}

type TranscriptIssue struct {
	SegmentID int      `json:"segment_id"`
	Start     float64  `json:"start"`
	End       float64  `json:"end"`
	Text      string   `json:"text"`
	Reason    string   `json:"reason"`
	Hints     []string `json:"hints,omitempty"`
}

type CutCandidate struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Start      float64   `json:"start"`
	End        float64   `json:"end"`
	Text       string    `json:"text,omitempty"`
	Confidence float64   `json:"confidence,omitempty"`
	Source     string    `json:"source"`
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason"`
	Safety     CutSafety `json:"safety"`
}

type CutSafety struct {
	PrePadMS         int  `json:"pre_pad_ms"`
	PostPadMS        int  `json:"post_pad_ms"`
	MergedWithNext   bool `json:"merged_with_next"`
	ProtectedContext bool `json:"protected_context"`
}

type CutPlan struct {
	SchemaVersion        string         `json:"schema_version"`
	InputDurationSeconds float64        `json:"input_duration_seconds"`
	Preset               string         `json:"preset"`
	Cuts                 []PlannedCut   `json:"cuts"`
	KeptSegments         []TimeSegment  `json:"kept_segments"`
	Candidates           []CutCandidate `json:"candidates"`
}

type PlannedCut struct {
	ID    string  `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Type  string  `json:"type"`
	Label string  `json:"label,omitempty"`
}

type TimeSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type RunManifest struct {
	SchemaVersion string                 `json:"schema_version"`
	RunID         string                 `json:"run_id"`
	CreatedAt     string                 `json:"created_at"`
	Input         ManifestInput          `json:"input"`
	Outputs       map[string]string      `json:"outputs"`
	Config        map[string]string      `json:"config"`
	Dependencies  map[string]interface{} `json:"dependencies"`
	Stats         ManifestStats          `json:"stats"`
}

type ManifestInput struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
}

type ManifestStats struct {
	InputDurationSeconds   float64 `json:"input_duration_seconds"`
	OutputDurationSeconds  float64 `json:"output_duration_seconds"`
	RemovedDurationSeconds float64 `json:"removed_duration_seconds"`
	CutCount               int     `json:"cut_count"`
}

type Preset struct {
	Name                             string
	MinFillerConfidence              float64
	MinSilenceSeconds                float64
	PrePadMS                         int
	PostPadMS                        int
	MergeGapMS                       int
	MaxCutSecondsWithoutConfirmation float64
	MinKeptSegmentMS                 int
	EdgeGuardMS                      int
}

type ProcessOptions struct {
	Input         string
	Out           string
	Preset        string
	Variants      string
	Language      string
	FillerList    string
	ProtectList   string
	MinSilence    float64
	PrePadMS      int
	PostPadMS     int
	RenderMode    string
	AudioEnhance  string
	Loudnorm      bool
	Denoise       bool
	TrimEdges     bool
	BurnSubtitles bool
	SubtitleStyle string
	DryRun        bool
	Provider      string
	Model         string
	KeepAudio     bool
	Timestamps    string
	Transcript    string
	UseCache      bool
	Resume        bool
	ProgressMode  string
}

type OutputPaths struct {
	Base           string
	Video          string
	Cuts           string
	TranscriptSRT  string
	TranscriptASS  string
	TranscriptJSON string
	ReviewJSON     string
	ReviewMD       string
	CorrectedJSON  string
	CorrectedSRT   string
	CorrectedASS   string
	PreviewMD      string
	PreviewHTML    string
	Report         string
	Manifest       string
	Audio          string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, rest, err := parseGlobalOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, "参数错误："+err.Error())
		return exitInvalidInput
	}
	if len(rest) == 0 {
		printHelp(stdout)
		return exitOK
	}

	switch rest[0] {
	case "help", "-h", "--help":
		printHelp(stdout)
		return exitOK
	case "install":
		return commandInstall(rest[1:], opts, stdout, stderr)
	case "models":
		return commandModels(rest[1:], opts, stdout, stderr)
	case "doctor":
		return commandDoctor(rest[1:], opts, stdout, stderr)
	case "probe":
		return commandProbe(rest[1:], opts, stdout, stderr)
	case "transcribe":
		return commandTranscribe(rest[1:], opts, stdout, stderr)
	case "transcript":
		return commandTranscript(rest[1:], opts, stdout, stderr)
	case "analyze":
		return commandAnalyze(rest[1:], opts, stdout, stderr)
	case "render":
		return commandRender(rest[1:], opts, stdout, stderr)
	case "batch":
		return commandBatch(rest[1:], opts, stdout, stderr)
	case "explain":
		return commandExplain(rest[1:], opts, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "未知命令：%s\n", rest[0])
		printHelp(stderr)
		return exitInvalidInput
	}
}

func parseGlobalOptions(args []string) (GlobalOptions, []string, error) {
	opts := GlobalOptions{Progress: "plain", LogLevel: "info"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return opts, args[i+1:], nil
		}
		if !strings.HasPrefix(arg, "-") {
			return opts, args[i:], nil
		}
		key, val, hasVal := splitFlag(arg)
		needValue := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s 需要值", key)
			}
			i++
			return args[i], nil
		}
		switch key {
		case "--config":
			v, err := needValue()
			if err != nil {
				return opts, nil, err
			}
			opts.Config = v
		case "--home":
			v, err := needValue()
			if err != nil {
				return opts, nil, err
			}
			opts.Home = v
		case "--json":
			opts.JSON = true
		case "--progress":
			v, err := needValue()
			if err != nil {
				return opts, nil, err
			}
			opts.Progress = v
		case "--log-level":
			v, err := needValue()
			if err != nil {
				return opts, nil, err
			}
			opts.LogLevel = v
		case "--no-color":
			opts.NoColor = true
		case "--yes":
			opts.Yes = true
		default:
			return opts, nil, fmt.Errorf("未知全局参数 %s", key)
		}
	}
	return opts, nil, nil
}

func splitFlag(arg string) (key, value string, hasValue bool) {
	if idx := strings.Index(arg, "="); idx >= 0 {
		return arg[:idx], arg[idx+1:], true
	}
	return arg, "", false
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, `AutoClip - 本地优先视频粗剪 CLI

用法：
  autoclip [全局参数] <命令> [参数]

命令：
  install      安装本地依赖和选择 whisper 模型档位
  models       查看或刷新本地模型清单
  doctor       检查运行环境
  probe        探测媒体信息
  transcribe   本地转写媒体
  transcript   生成 Agent 校字包、应用校字结果
  analyze      生成剪辑方案，不渲染视频
  render       生成剪辑后视频
  batch        批量处理目录
  explain      读取 manifest 并总结结果

全局参数：
  --home PATH            指定 AutoClip 缓存目录
  --json                 输出机器可读 JSON
  --yes                  非交互确认安全操作
  --progress MODE        plain|jsonl|none
  --log-level LEVEL      debug|info|warn|error`)
}

func commandInstall(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	fs := newFlagSet("install")
	profile := fs.String("profile", "", "lite|standard|fast-high|best")
	modelName := fs.String("model", "", "直接指定 whisper.cpp 模型名")
	with := fs.String("with", "ffmpeg,whispercpp,model", "要安装的依赖")
	repair := fs.Bool("repair", false, "重新下载并修复依赖")
	if err := fs.Parse(reorderInterspersedFlags(args, map[string]bool{"repair": true})); err != nil {
		fmt.Fprintln(stderr, "install 参数错误："+err.Error())
		return exitInvalidInput
	}
	manifest := loadDependencyManifest()
	model, err := chooseModel(manifest, *profile, *modelName, opts, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitInvalidInput
	}

	home := autoclipHome(opts.Home)
	if err := ensureHome(home); err != nil {
		fmt.Fprintln(stderr, "无法创建 AutoClip 缓存目录："+err.Error())
		return exitPermissionOrDisk
	}

	lock, _ := readInstallLock(home)
	lock.SchemaVersion = schemaVersion
	lock.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	lock.Home = home
	lock.Profile = model.Profile
	if lock.Tools == nil {
		lock.Tools = map[string]InstalledDependency{}
	}

	parts := commaSet(*with)
	if parts["model"] && !opts.JSON {
		printModelDownloadNotice(model, stdout)
	}
	if parts["ffmpeg"] {
		ffmpeg, ffprobe := discoverFFmpeg(home)
		if ffmpeg != "" {
			lock.Tools["ffmpeg"] = InstalledDependency{Name: "ffmpeg", Path: ffmpeg, Source: dependencySource(ffmpeg, home)}
			fmt.Fprintf(stdout, "已找到 FFmpeg：%s\n", ffmpeg)
		} else {
			inst, err := ensureBinaryFromManifest(home, manifest, "ffmpeg", *repair, stdout)
			if err != nil {
				fmt.Fprintln(stderr, "未找到 FFmpeg，且 manifest 未提供可安装的当前平台二进制："+err.Error())
			} else {
				lock.Tools["ffmpeg"] = inst
				fmt.Fprintf(stdout, "已安装 FFmpeg：%s\n", inst.Path)
			}
		}
		if ffprobe != "" {
			lock.Tools["ffprobe"] = InstalledDependency{Name: "ffprobe", Path: ffprobe, Source: dependencySource(ffprobe, home)}
			fmt.Fprintf(stdout, "已找到 ffprobe：%s\n", ffprobe)
		} else {
			inst, err := ensureBinaryFromManifest(home, manifest, "ffprobe", *repair, stdout)
			if err != nil {
				fmt.Fprintln(stdout, "未找到 ffprobe，将使用 FFmpeg 输出作为探测 fallback。")
			} else {
				lock.Tools["ffprobe"] = inst
				fmt.Fprintf(stdout, "已安装 ffprobe：%s\n", inst.Path)
			}
		}
	}

	if parts["whispercpp"] {
		whisperPath, err := ensureWhisperCLI(home, manifest, opts, *repair, stdout, stderr)
		if err != nil {
			fmt.Fprintln(stderr, "whisper.cpp 安装失败："+err.Error())
			return exitDependencyFailure
		}
		lock.Tools["whisper-cli"] = InstalledDependency{Name: "whisper-cli", Path: whisperPath, Version: manifest.WhisperCPP.Version, Source: dependencySource(whisperPath, home)}
	}

	if parts["model"] {
		inst, err := ensureModel(home, model, *repair, stdout)
		if err != nil {
			fmt.Fprintln(stderr, "模型安装失败："+err.Error())
			return exitDependencyFailure
		}
		lock.Model = inst
	}

	if err := writeInstallLock(home, lock); err != nil {
		fmt.Fprintln(stderr, "写入 dependencies.lock.json 失败："+err.Error())
		return exitPermissionOrDisk
	}
	if opts.JSON {
		writeJSON(stdout, map[string]interface{}{"ok": true, "home": home, "profile": model.Profile, "model": lock.Model})
		return exitOK
	}
	fmt.Fprintf(stdout, "\n安装完成。档位：%s，模型：%s\n", model.Profile, model.Name)
	return exitOK
}

func commandModels(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "models 需要子命令：list 或 refresh")
		return exitInvalidInput
	}
	switch args[0] {
	case "list":
		manifest := loadDependencyManifest()
		if opts.JSON {
			writeJSON(stdout, manifest.Models)
			return exitOK
		}
		printModelTable(stdout, manifest.Models)
		return exitOK
	case "refresh":
		if !opts.Yes && isTerminal(os.Stdin) {
			fmt.Fprint(stdout, "将联网读取 whisper.cpp 官方模型表并刷新本地清单，继续？[y/N] ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if strings.ToLower(strings.TrimSpace(line)) != "y" {
				fmt.Fprintln(stdout, "已取消。")
				return exitOK
			}
		} else if !opts.Yes {
			fmt.Fprintln(stderr, "models refresh 会联网。非交互环境请显式传 --yes。")
			return exitInvalidInput
		}
		manifest := loadDependencyManifest()
		refreshed, err := refreshModels(manifest)
		if err != nil {
			fmt.Fprintln(stderr, "刷新失败："+err.Error())
			return exitGeneralFailure
		}
		home := autoclipHome(opts.Home)
		if err := ensureHome(home); err != nil {
			fmt.Fprintln(stderr, "无法创建缓存目录："+err.Error())
			return exitPermissionOrDisk
		}
		path := filepath.Join(home, "dependency-manifest.local.json")
		if err := writeJSONFile(path, refreshed); err != nil {
			fmt.Fprintln(stderr, "写入本地清单失败："+err.Error())
			return exitPermissionOrDisk
		}
		fmt.Fprintf(stdout, "已刷新本地模型清单：%s\n", path)
		return exitOK
	default:
		fmt.Fprintf(stderr, "未知 models 子命令：%s\n", args[0])
		return exitInvalidInput
	}
}

func commandDoctor(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	fs := newFlagSet("doctor")
	if err := fs.Parse(reorderInterspersedFlags(args, nil)); err != nil {
		fmt.Fprintln(stderr, "doctor 参数错误："+err.Error())
		return exitInvalidInput
	}
	home := autoclipHome(opts.Home)
	status := map[string]interface{}{"ok": true, "home": home, "checks": []map[string]interface{}{}}
	add := func(name string, ok bool, detail string) {
		status["checks"] = append(status["checks"].([]map[string]interface{}), map[string]interface{}{"name": name, "ok": ok, "detail": detail})
		if !ok {
			status["ok"] = false
		}
	}
	if err := ensureHome(home); err != nil {
		add("缓存目录可写", false, err.Error())
	} else {
		add("缓存目录可写", true, home)
	}
	ffmpeg, ffprobe := discoverFFmpeg(home)
	add("FFmpeg", ffmpeg != "", valueOr(ffmpeg, "未找到"))
	if ffprobe != "" {
		add("ffprobe", true, ffprobe)
	} else if ffmpeg != "" {
		add("ffprobe", true, "未找到，已启用 FFmpeg fallback")
	} else {
		add("ffprobe", false, "未找到，且 FFmpeg fallback 不可用")
	}
	whisper := discoverExecutable(home, "whisper-cli")
	add("whisper-cli", whisper != "", valueOr(whisper, "未找到，请运行 autoclip install --profile <档位>"))
	lock, err := readInstallLock(home)
	if err != nil || lock.Model.Path == "" {
		add("本地模型", false, "未安装模型，请运行 autoclip install --profile lite|standard|fast-high|best")
	} else if _, err := os.Stat(lock.Model.Path); err != nil {
		add("本地模型", false, "lock 中的模型不存在："+lock.Model.Path)
	} else {
		add("本地模型", true, lock.Model.Name+" ("+lock.Model.Size+")")
	}
	if opts.JSON {
		writeJSON(stdout, status)
	} else {
		fmt.Fprintln(stdout, "AutoClip 环境检查")
		for _, c := range status["checks"].([]map[string]interface{}) {
			mark := "OK"
			if !c["ok"].(bool) {
				mark = "WARN"
			}
			fmt.Fprintf(stdout, "- [%s] %s：%s\n", mark, c["name"], c["detail"])
		}
	}
	if status["ok"].(bool) {
		return exitOK
	}
	return exitDependencyFailure
}

func commandProbe(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	fs := newFlagSet("probe")
	if err := fs.Parse(reorderInterspersedFlags(args, nil)); err != nil {
		fmt.Fprintln(stderr, "probe 参数错误："+err.Error())
		return exitInvalidInput
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：autoclip probe input.mp4")
		return exitInvalidInput
	}
	info, err := probeMedia(fs.Arg(0), autoclipHome(opts.Home))
	if err != nil {
		fmt.Fprintln(stderr, "媒体探测失败："+err.Error())
		return exitUnsupportedMedia
	}
	if opts.JSON {
		writeJSON(stdout, info)
	} else {
		fmt.Fprintf(stdout, "文件：%s\n时长：%s\n视频：%s %dx%d %s\n音频：%s %d Hz\n探测工具：%s\n", info.Path, formatDuration(info.DurationSeconds), info.VideoCodec, info.Width, info.Height, info.FrameRate, info.AudioCodec, info.AudioSampleRate, info.ProbeTool)
	}
	return exitOK
}

func commandTranscribe(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	fs := newFlagSet("transcribe")
	provider := fs.String("provider", "local", "local|openai|auto")
	model := fs.String("model", "", "模型名或模型路径")
	language := fs.String("language", "auto", "语言代码")
	out := fs.String("out", "", "输出目录")
	timestamps := fs.String("timestamps", "segment", "word|segment")
	prompt := fs.String("prompt", "", "转写提示")
	reviewPack := fs.Bool("review-pack", false, "生成给 Agent 校字用的 review 包")
	context := fs.String("context", "", "给 Agent 校字的素材上下文")
	glossary := fs.String("glossary", "", "逗号分隔专名/术语，供 Agent 校字参考")
	subtitleStyle := fs.String("subtitle-style", "default", "default|large")
	keepAudio := fs.Bool("keep-audio", false, "保留提取出的 wav")
	if err := fs.Parse(reorderInterspersedFlags(args, map[string]bool{"keep-audio": true, "review-pack": true})); err != nil {
		fmt.Fprintln(stderr, "transcribe 参数错误："+err.Error())
		return exitInvalidInput
	}
	_ = prompt
	if *timestamps != "segment" && *timestamps != "word" {
		fmt.Fprintln(stderr, "timestamps 可选：segment 或 word")
		return exitInvalidInput
	}
	if *subtitleStyle != "default" && *subtitleStyle != "large" {
		fmt.Fprintln(stderr, "subtitle-style 可选：default 或 large")
		return exitInvalidInput
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：autoclip transcribe input.mp4 --provider local")
		return exitInvalidInput
	}
	p := ProcessOptions{Input: fs.Arg(0), Out: *out, Provider: *provider, Model: *model, Language: *language, KeepAudio: *keepAudio, Timestamps: *timestamps}
	paths := makeOutputPaths(p.Input, p.Out, false, "accurate")
	transcript, err := transcribeLocal(p, opts, paths, stdout)
	if err != nil {
		fmt.Fprintln(stderr, "转写失败："+err.Error())
		if errors.Is(err, errCloudReserved) {
			return exitCloudConfigRequired
		}
		return exitTranscriptionFailure
	}
	if err := writeJSONFile(paths.TranscriptJSON, transcript); err != nil {
		fmt.Fprintln(stderr, "写入 transcript JSON 失败："+err.Error())
		return exitPermissionOrDisk
	}
	if err := writeSRT(paths.TranscriptSRT, transcript); err != nil {
		fmt.Fprintln(stderr, "写入 SRT 失败："+err.Error())
		return exitPermissionOrDisk
	}
	if err := writeASS(paths.TranscriptASS, transcript, *subtitleStyle); err != nil {
		fmt.Fprintln(stderr, "写入 ASS 失败："+err.Error())
		return exitPermissionOrDisk
	}
	result := map[string]interface{}{"ok": true, "transcript": paths.TranscriptJSON, "srt": paths.TranscriptSRT, "segments": len(transcript.Segments)}
	result["ass"] = paths.TranscriptASS
	if *reviewPack {
		pack := buildTranscriptReviewPack(paths.TranscriptJSON, transcript, *context, splitList(*glossary))
		if err := writeJSONFile(paths.ReviewJSON, pack); err != nil {
			fmt.Fprintln(stderr, "写入 review JSON 失败："+err.Error())
			return exitPermissionOrDisk
		}
		if err := writeTranscriptReviewMD(paths.ReviewMD, pack); err != nil {
			fmt.Fprintln(stderr, "写入 review Markdown 失败："+err.Error())
			return exitPermissionOrDisk
		}
		result["review_json"] = paths.ReviewJSON
		result["review_md"] = paths.ReviewMD
	}
	writeJSON(stdout, result)
	return exitOK
}

func commandTranscript(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "transcript 需要子命令：review 或 apply")
		return exitInvalidInput
	}
	switch args[0] {
	case "review":
		return commandTranscriptReview(args[1:], opts, stdout, stderr)
	case "apply":
		return commandTranscriptApply(args[1:], opts, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "未知 transcript 子命令：%s\n", args[0])
		return exitInvalidInput
	}
}

func commandTranscriptReview(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	fs := newFlagSet("transcript review")
	out := fs.String("out", "", "输出目录或文件前缀")
	context := fs.String("context", "", "素材上下文，帮助 Agent 判断同音词和专名")
	glossary := fs.String("glossary", "", "逗号分隔专名/术语")
	if err := fs.Parse(reorderInterspersedFlags(args, nil)); err != nil {
		fmt.Fprintln(stderr, "transcript review 参数错误："+err.Error())
		return exitInvalidInput
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：autoclip transcript review input.transcript.json --context \"素材说明\"")
		return exitInvalidInput
	}
	transcriptPath := fs.Arg(0)
	tr, err := readTranscriptFile(transcriptPath, "")
	if err != nil {
		fmt.Fprintln(stderr, "读取 transcript 失败："+err.Error())
		return exitInvalidInput
	}
	paths := makeTranscriptSidecarPaths(transcriptPath, *out)
	pack := buildTranscriptReviewPack(transcriptPath, tr, *context, splitList(*glossary))
	if err := writeJSONFile(paths.ReviewJSON, pack); err != nil {
		fmt.Fprintln(stderr, "写入 review JSON 失败："+err.Error())
		return exitPermissionOrDisk
	}
	if err := writeTranscriptReviewMD(paths.ReviewMD, pack); err != nil {
		fmt.Fprintln(stderr, "写入 review Markdown 失败："+err.Error())
		return exitPermissionOrDisk
	}
	if opts.JSON {
		writeJSON(stdout, map[string]interface{}{"ok": true, "review_json": paths.ReviewJSON, "review_md": paths.ReviewMD, "issues": len(pack.Issues)})
	} else {
		fmt.Fprintf(stdout, "已生成 Agent 校字包：%s\n校字说明：%s\n可疑片段：%d\n", paths.ReviewJSON, paths.ReviewMD, len(pack.Issues))
	}
	return exitOK
}

func commandTranscriptApply(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	fs := newFlagSet("transcript apply")
	out := fs.String("out", "", "输出目录或文件前缀")
	subtitleStyle := fs.String("subtitle-style", "default", "default|large")
	if err := fs.Parse(reorderInterspersedFlags(args, nil)); err != nil {
		fmt.Fprintln(stderr, "transcript apply 参数错误："+err.Error())
		return exitInvalidInput
	}
	if *subtitleStyle != "default" && *subtitleStyle != "large" {
		fmt.Fprintln(stderr, "subtitle-style 可选：default 或 large")
		return exitInvalidInput
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "用法：autoclip transcript apply original.transcript.json corrected.json")
		return exitInvalidInput
	}
	originalPath := fs.Arg(0)
	original, err := readTranscriptFile(originalPath, "")
	if err != nil {
		fmt.Fprintln(stderr, "读取原 transcript 失败："+err.Error())
		return exitInvalidInput
	}
	proposed, err := readTranscriptFile(fs.Arg(1), original.Language)
	if err != nil {
		fmt.Fprintln(stderr, "读取校字结果失败："+err.Error())
		return exitInvalidInput
	}
	corrected, changes, err := applyTranscriptCorrection(original, proposed, fs.Arg(1))
	if err != nil {
		fmt.Fprintln(stderr, "应用校字结果失败："+err.Error())
		return exitInvalidInput
	}
	paths := makeTranscriptSidecarPaths(originalPath, *out)
	if err := writeJSONFile(paths.CorrectedJSON, corrected); err != nil {
		fmt.Fprintln(stderr, "写入 corrected JSON 失败："+err.Error())
		return exitPermissionOrDisk
	}
	if err := writeSRT(paths.CorrectedSRT, corrected); err != nil {
		fmt.Fprintln(stderr, "写入 corrected SRT 失败："+err.Error())
		return exitPermissionOrDisk
	}
	if err := writeASS(paths.CorrectedASS, corrected, *subtitleStyle); err != nil {
		fmt.Fprintln(stderr, "写入 corrected ASS 失败："+err.Error())
		return exitPermissionOrDisk
	}
	if opts.JSON {
		writeJSON(stdout, map[string]interface{}{"ok": true, "corrected_json": paths.CorrectedJSON, "corrected_srt": paths.CorrectedSRT, "corrected_ass": paths.CorrectedASS, "changes": changes})
	} else {
		fmt.Fprintf(stdout, "已应用 Agent 校字结果：%s\n字幕：%s\nASS：%s\n变更片段：%d\n", paths.CorrectedJSON, paths.CorrectedSRT, paths.CorrectedASS, changes)
	}
	return exitOK
}

func commandAnalyze(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	p, code, ok := parseProcessFlags("analyze", args, opts, stderr)
	if !ok {
		return code
	}
	result, err := processOne(p, opts, false, stdout)
	if err != nil {
		fmt.Fprintln(stderr, "分析失败："+err.Error())
		return classifyProcessError(err, exitPlanningFailure)
	}
	if opts.JSON {
		writeJSON(stdout, result)
	} else {
		fmt.Fprintf(stdout, "分析完成：剪辑点 %d 个，预计移除 %s。\n报告：%s\n", result["cut_count"], formatDuration(result["removed_seconds"].(float64)), result["report"])
	}
	return exitOK
}

func commandRender(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	p, code, ok := parseProcessFlags("render", args, opts, stderr)
	if !ok {
		return code
	}
	if p.Variants != "" && !p.DryRun {
		result, err := processVariants(p, opts, stdout)
		if err != nil {
			fmt.Fprintln(stderr, "多档输出失败："+err.Error())
			return classifyProcessError(err, exitRenderingFailure)
		}
		if opts.JSON {
			writeJSON(stdout, result)
		} else {
			fmt.Fprintf(stdout, "多档输出完成：成功 %d / %d。目录：%s\n", result["success"], result["total"], result["out_dir"])
		}
		return exitOK
	}
	if p.DryRun {
		result, err := processOne(p, opts, false, stdout)
		if err != nil {
			fmt.Fprintln(stderr, "dry-run 分析失败："+err.Error())
			return classifyProcessError(err, exitPlanningFailure)
		}
		writeJSON(stdout, result)
		return exitOK
	}
	result, err := processOne(p, opts, true, stdout)
	if err != nil {
		fmt.Fprintln(stderr, "渲染失败："+err.Error())
		return classifyProcessError(err, exitRenderingFailure)
	}
	if opts.JSON {
		writeJSON(stdout, result)
	} else {
		fmt.Fprintf(stdout, "渲染完成：%s\n剪辑点：%d，移除：%s\n报告：%s\n", result["output"], result["cut_count"], formatDuration(result["removed_seconds"].(float64)), result["report"])
	}
	return exitOK
}

func commandBatch(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	fs := newFlagSet("batch")
	preset := fs.String("preset", "conservative", "conservative|balanced|aggressive")
	language := fs.String("language", "auto", "语言代码")
	recursive := fs.Bool("recursive", false, "递归处理子目录")
	out := fs.String("out", "", "输出目录")
	provider := fs.String("provider", "local", "local|openai|auto")
	renderMode := fs.String("render-mode", "accurate", "accurate|audio-only")
	timestamps := fs.String("timestamps", "segment", "word|segment")
	audioEnhance := fs.String("audio-enhance", "none", "none|voice")
	loudnorm := fs.Bool("loudnorm", false, "启用 loudnorm 音量标准化")
	denoise := fs.Bool("denoise", false, "启用轻量降噪和静音门限")
	trimEdges := fs.Bool("trim-edges", false, "裁掉开头和结尾长静音")
	burnSubtitles := fs.Bool("burn-subtitles", false, "将 ASS 字幕烧录到视频")
	subtitleStyle := fs.String("subtitle-style", "default", "default|large")
	cache := fs.Bool("cache", true, "启用 transcript 缓存复用")
	resume := fs.Bool("resume", false, "已有完整产物时跳过处理")
	if err := fs.Parse(reorderInterspersedFlags(args, map[string]bool{"recursive": true, "loudnorm": true, "denoise": true, "trim-edges": true, "burn-subtitles": true, "cache": true, "resume": true})); err != nil {
		fmt.Fprintln(stderr, "batch 参数错误："+err.Error())
		return exitInvalidInput
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：autoclip batch ./videos --preset conservative")
		return exitInvalidInput
	}
	if _, err := presetByName(*preset); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitInvalidInput
	}
	if *timestamps != "segment" && *timestamps != "word" {
		fmt.Fprintln(stderr, "timestamps 可选：segment 或 word")
		return exitInvalidInput
	}
	if *audioEnhance != "none" && *audioEnhance != "voice" {
		fmt.Fprintln(stderr, "audio-enhance 可选：none 或 voice")
		return exitInvalidInput
	}
	if *subtitleStyle != "default" && *subtitleStyle != "large" {
		fmt.Fprintln(stderr, "subtitle-style 可选：default 或 large")
		return exitInvalidInput
	}
	files, err := listMediaFiles(fs.Arg(0), *recursive)
	if err != nil {
		fmt.Fprintln(stderr, "读取目录失败："+err.Error())
		return exitInvalidInput
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "目录中没有支持的视频/音频文件。")
		return exitUnsupportedMedia
	}
	var rows []map[string]interface{}
	success := 0
	for _, file := range files {
		p := ProcessOptions{
			Input: file, Out: *out, Preset: *preset, Language: *language, Provider: *provider, RenderMode: *renderMode,
			Timestamps: *timestamps, AudioEnhance: *audioEnhance, Loudnorm: *loudnorm, Denoise: *denoise, TrimEdges: *trimEdges,
			BurnSubtitles: *burnSubtitles, SubtitleStyle: *subtitleStyle, UseCache: *cache, Resume: *resume,
		}
		if *resume && outputsComplete(makeOutputPaths(file, *out, true, *renderMode)) {
			row := map[string]interface{}{"input": file, "ok": true, "skipped": true, "output": makeOutputPaths(file, *out, true, *renderMode).Video, "removed_seconds": 0.0}
			rows = append(rows, row)
			success++
			continue
		}
		result, err := processOne(p, opts, true, stdout)
		row := map[string]interface{}{"input": file}
		if err != nil {
			row["ok"] = false
			row["error"] = err.Error()
		} else {
			row["ok"] = true
			row["output"] = result["output"]
			row["cut_count"] = result["cut_count"]
			row["removed_seconds"] = result["removed_seconds"]
			success++
		}
		rows = append(rows, row)
	}
	reportPath := filepath.Join(batchReportDir(fs.Arg(0), *out), "batch.report.md")
	_ = os.MkdirAll(filepath.Dir(reportPath), 0o755)
	if err := writeBatchReport(reportPath, rows); err != nil {
		fmt.Fprintln(stderr, "写入批处理报告失败："+err.Error())
		return exitPermissionOrDisk
	}
	summary := map[string]interface{}{"ok": success == len(files), "total": len(files), "success": success, "failed": len(files) - success, "report": reportPath, "items": rows}
	if opts.JSON {
		writeJSON(stdout, summary)
	} else {
		fmt.Fprintf(stdout, "批处理完成：成功 %d / %d。报告：%s\n", success, len(files), reportPath)
	}
	if success == len(files) {
		return exitOK
	}
	return exitGeneralFailure
}

func commandExplain(args []string, opts GlobalOptions, stdout, stderr io.Writer) int {
	fs := newFlagSet("explain")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, "explain 参数错误："+err.Error())
		return exitInvalidInput
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：autoclip explain ./input.autoclip.manifest.json")
		return exitInvalidInput
	}
	var manifest RunManifest
	if err := readJSONFile(fs.Arg(0), &manifest); err != nil {
		fmt.Fprintln(stderr, "读取 manifest 失败："+err.Error())
		return exitInvalidInput
	}
	summary := map[string]interface{}{
		"ok": true, "input": manifest.Input.Path, "output": manifest.Outputs["video"],
		"cut_count": manifest.Stats.CutCount, "removed_seconds": manifest.Stats.RemovedDurationSeconds,
		"report": manifest.Outputs["report"], "warnings": []string{},
	}
	if opts.JSON {
		writeJSON(stdout, summary)
	} else {
		fmt.Fprintf(stdout, "AutoClip 已处理：%s\n输出：%s\n剪辑点：%d，移除：%s\n报告：%s\n", manifest.Input.Path, manifest.Outputs["video"], manifest.Stats.CutCount, formatDuration(manifest.Stats.RemovedDurationSeconds), manifest.Outputs["report"])
	}
	return exitOK
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func reorderInterspersedFlags(args []string, boolFlags map[string]bool) []string {
	var flagArgs []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !isFlagToken(arg) {
			positionals = append(positionals, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		name, hasInlineValue := flagTokenName(arg)
		if hasInlineValue || boolFlags[name] {
			continue
		}
		if i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return append(flagArgs, positionals...)
}

func isFlagToken(arg string) bool {
	return strings.HasPrefix(arg, "-") && arg != "-"
}

func flagTokenName(arg string) (string, bool) {
	trimmed := strings.TrimLeft(arg, "-")
	if idx := strings.Index(trimmed, "="); idx >= 0 {
		return trimmed[:idx], true
	}
	return trimmed, false
}

func parseProcessFlags(name string, args []string, opts GlobalOptions, stderr io.Writer) (ProcessOptions, int, bool) {
	fs := newFlagSet(name)
	p := ProcessOptions{}
	fs.StringVar(&p.Preset, "preset", "balanced", "conservative|balanced|aggressive")
	fs.StringVar(&p.Variants, "variants", "", "逗号分隔多档输出预设，如 conservative,balanced,aggressive")
	fs.StringVar(&p.Language, "language", "auto", "语言代码")
	fs.StringVar(&p.FillerList, "filler", "", "逗号分隔口癖词")
	fs.StringVar(&p.ProtectList, "protect", "", "逗号分隔保护词")
	fs.Float64Var(&p.MinSilence, "min-silence", 0, "最短静音秒数")
	fs.IntVar(&p.PrePadMS, "pre-pad", -1, "剪辑前保留毫秒")
	fs.IntVar(&p.PostPadMS, "post-pad", -1, "剪辑后保留毫秒")
	fs.StringVar(&p.RenderMode, "render-mode", "accurate", "accurate|audio-only")
	fs.StringVar(&p.AudioEnhance, "audio-enhance", "none", "none|voice")
	fs.BoolVar(&p.Loudnorm, "loudnorm", false, "启用 loudnorm 音量标准化")
	fs.BoolVar(&p.Denoise, "denoise", false, "启用轻量降噪和静音门限")
	fs.BoolVar(&p.TrimEdges, "trim-edges", false, "裁掉开头和结尾长静音")
	fs.BoolVar(&p.BurnSubtitles, "burn-subtitles", false, "将 ASS 字幕烧录到视频")
	fs.StringVar(&p.SubtitleStyle, "subtitle-style", "default", "default|large")
	fs.StringVar(&p.Out, "out", "", "输出路径或目录")
	fs.BoolVar(&p.DryRun, "dry-run", false, "只分析不渲染")
	fs.StringVar(&p.Provider, "provider", "local", "local|openai|auto")
	fs.StringVar(&p.Model, "model", "", "模型名或模型路径")
	fs.StringVar(&p.Transcript, "transcript", "", "使用已校字 transcript JSON，跳过重新转写")
	fs.StringVar(&p.Timestamps, "timestamps", "segment", "word|segment")
	fs.BoolVar(&p.UseCache, "cache", false, "启用 transcript 缓存复用")
	fs.BoolVar(&p.Resume, "resume", false, "已有完整产物时跳过处理")
	fs.BoolVar(&p.KeepAudio, "keep-audio", false, "保留提取音频")
	p.ProgressMode = opts.Progress
	if err := fs.Parse(reorderInterspersedFlags(args, map[string]bool{"dry-run": true, "keep-audio": true, "loudnorm": true, "denoise": true, "trim-edges": true, "burn-subtitles": true, "cache": true, "resume": true})); err != nil {
		fmt.Fprintf(stderr, "%s 参数错误：%s\n", name, err)
		return p, exitInvalidInput, false
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(stderr, "用法：autoclip %s input.mp4 --preset balanced\n", name)
		return p, exitInvalidInput, false
	}
	p.Input = fs.Arg(0)
	if p.Preset == "" {
		p.Preset = "balanced"
	}
	if _, err := presetByName(p.Preset); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return p, exitInvalidInput, false
	}
	if p.Timestamps != "segment" && p.Timestamps != "word" {
		fmt.Fprintln(stderr, "timestamps 可选：segment 或 word")
		return p, exitInvalidInput, false
	}
	if p.AudioEnhance != "none" && p.AudioEnhance != "voice" {
		fmt.Fprintln(stderr, "audio-enhance 可选：none 或 voice")
		return p, exitInvalidInput, false
	}
	if p.SubtitleStyle != "default" && p.SubtitleStyle != "large" {
		fmt.Fprintln(stderr, "subtitle-style 可选：default 或 large")
		return p, exitInvalidInput, false
	}
	return p, exitOK, true
}

func chooseModel(manifest DependencyManifest, profile, modelName string, opts GlobalOptions, stdout, stderr io.Writer) (ModelEntry, error) {
	if modelName != "" {
		for _, m := range manifest.Models {
			if m.Name == modelName {
				return m, nil
			}
		}
		return ModelEntry{}, fmt.Errorf("未知模型 %s，请先运行 autoclip models list", modelName)
	}
	if profile != "" {
		for _, m := range manifest.Models {
			if m.Profile == profile {
				return m, nil
			}
		}
		return ModelEntry{}, fmt.Errorf("未知档位 %s，可选：lite, standard, fast-high, best", profile)
	}
	if isTerminal(os.Stdin) && !opts.JSON {
		printModelTable(stdout, manifest.Models)
		fmt.Fprint(stdout, "\n请选择模型档位 [lite/standard/fast-high/best]：")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		selected := strings.TrimSpace(line)
		if selected == "" {
			return ModelEntry{}, errors.New("未选择模型档位，请使用 --profile lite|standard|fast-high|best")
		}
		return chooseModel(manifest, selected, "", opts, stdout, stderr)
	}
	return ModelEntry{}, errors.New("首次安装必须显式选择本地模型档位：--profile lite|standard|fast-high|best。不会静默下载大模型")
}

func printModelTable(w io.Writer, models []ModelEntry) {
	fmt.Fprintln(w, "可选本地模型档位：")
	fmt.Fprintln(w, "档位          模型                     大小       速度/质量                  备用源  用途")
	for _, m := range models {
		fmt.Fprintf(w, "%-13s %-24s %-9s %-24s %-7d %s\n", m.Profile, m.Name, m.Size, m.Speed+" / "+m.Quality, len(m.MirrorURLs), m.Use)
	}
	fmt.Fprintln(w, "\n下载来源与校验：")
	for _, m := range models {
		fmt.Fprintf(w, "- %s/%s：checksum %s:%s\n", m.Profile, m.Name, m.ChecksumType, m.Checksum)
		fmt.Fprintf(w, "  主源：%s\n", m.URL)
		for i, mirror := range m.MirrorURLs {
			fmt.Fprintf(w, "  备用%d：%s\n", i+1, mirror)
		}
	}
}

func printModelDownloadNotice(model ModelEntry, w io.Writer) {
	fmt.Fprintf(w, "准备安装本地模型档位：%s / %s（%s，%s）。\n", model.Profile, model.Name, model.Size, model.Use)
	fmt.Fprintln(w, "首次下载可能耗时较长，复杂网络下请预留时间；AutoClip 会依次尝试官方源和备用镜像，支持断点续传，并在完成后校验 checksum。")
	if len(model.MirrorURLs) > 0 {
		fmt.Fprintf(w, "已配置备用镜像：%d 个。若中途失败，重新运行同一条 install 命令会继续尝试。\n", len(model.MirrorURLs))
	}
}

func loadDependencyManifest() DependencyManifest {
	for _, p := range manifestSearchPaths() {
		var m DependencyManifest
		if err := readJSONFile(p, &m); err == nil && len(m.Models) > 0 {
			return m
		}
	}
	return defaultDependencyManifest()
}

func manifestSearchPaths() []string {
	var paths []string
	if p := os.Getenv("AUTOCLIP_DEPENDENCY_MANIFEST"); p != "" {
		paths = append(paths, p)
	}
	if home := os.Getenv("AUTOCLIP_HOME"); home != "" {
		paths = append(paths, filepath.Join(home, "dependency-manifest.local.json"))
	}
	wd, _ := os.Getwd()
	paths = append(paths,
		filepath.Join(wd, "release", "dependency-manifest.json"),
		filepath.Join(wd, "..", "release", "dependency-manifest.json"),
		filepath.Join(wd, "..", "..", "release", "dependency-manifest.json"),
	)
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		paths = append(paths,
			filepath.Join(dir, "dependency-manifest.json"),
			filepath.Join(dir, "..", "release", "dependency-manifest.json"),
		)
	}
	return paths
}

func whisperHFMirrorURL(fileName string) string {
	return "https://hf-mirror.com/ggerganov/whisper.cpp/resolve/main/" + fileName
}

func macPortsWhisperMirrorURLs(fileName string) []string {
	return []string{
		"https://mirrors.aliyun.com/macports/distfiles/whisper/" + fileName,
		"https://mirrors.mit.edu/macports/distfiles/whisper/" + fileName,
	}
}

func defaultModelMirrorURLs(fileName string) []string {
	mirrors := []string{whisperHFMirrorURL(fileName)}
	if fileName == "ggml-large-v3-turbo-q5_0.bin" {
		return mirrors
	}
	return append(mirrors, macPortsWhisperMirrorURLs(fileName)...)
}

func defaultDependencyManifest() DependencyManifest {
	return DependencyManifest{
		SchemaVersion:  schemaVersion,
		LastVerifiedAt: "2026-05-14",
		SourceNotes: []string{
			"whisper.cpp models README: https://github.com/ggml-org/whisper.cpp/blob/master/models/README.md",
			"OpenAI Whisper model card: https://github.com/openai/whisper/blob/main/model-card.md",
			"备用下载源包含 hf-mirror，以及 MacPorts 的 Aliyun/MIT 镜像；所有来源下载后都以 checksum 为准。",
		},
		WhisperCPP: SourceTool{
			Version: "v1.8.4", SourceURL: "https://github.com/ggml-org/whisper.cpp/archive/refs/tags/v1.8.4.tar.gz",
			ChecksumType: "sha256", Checksum: "b26f30e52c095ccb75da40b168437736605eb280de57381887bf9e2b65f31e66",
		},
		Models: []ModelEntry{
			{Profile: "lite", Name: "base", FileName: "ggml-base.bin", URL: "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin", MirrorURLs: defaultModelMirrorURLs("ggml-base.bin"), Size: "142 MiB", SizeBytes: 148897792, ChecksumType: "sha1", Checksum: "465707469ff3a37a2b9b8d8f89f2f99de7299dac", Quality: "基础", Speed: "快", Use: "试用、低配机器、短视频"},
			{Profile: "standard", Name: "small", FileName: "ggml-small.bin", URL: "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin", MirrorURLs: defaultModelMirrorURLs("ggml-small.bin"), Size: "466 MiB", SizeBytes: 488381464, ChecksumType: "sha1", Checksum: "55356645c2b361a969dfd0ef2c5a50d530afd8d5", Quality: "中等", Speed: "中等", Use: "日常粗剪"},
			{Profile: "fast-high", Name: "large-v3-turbo-q5_0", FileName: "ggml-large-v3-turbo-q5_0.bin", URL: "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo-q5_0.bin", MirrorURLs: defaultModelMirrorURLs("ggml-large-v3-turbo-q5_0.bin"), Size: "547 MiB", SizeBytes: 573571072, ChecksumType: "sha1", Checksum: "e050f7970618a659205450ad97eb95a18d69c9ee", Quality: "高", Speed: "较快", Use: "大多数正式使用，推荐"},
			{Profile: "best", Name: "large-v3", FileName: "ggml-large-v3.bin", URL: "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3.bin", MirrorURLs: defaultModelMirrorURLs("ggml-large-v3.bin"), Size: "2.9 GiB", SizeBytes: 3110000000, ChecksumType: "sha1", Checksum: "ad82bf6a9043ceed055076d0fd39f5f186ff8062", Quality: "最高", Speed: "慢", Use: "质量优先、重要素材", BestQuality: true},
		},
	}
}

func refreshModels(manifest DependencyManifest) (DependencyManifest, error) {
	resp, err := http.Get("https://raw.githubusercontent.com/ggml-org/whisper.cpp/master/models/README.md")
	if err != nil {
		return manifest, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return manifest, fmt.Errorf("官方模型表 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return manifest, err
	}
	text := string(body)
	for i, model := range manifest.Models {
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(model.Name) + `\s+([0-9.]+\s+\w+)\s+\x60([a-f0-9]{40})\x60`)
		if match := re.FindStringSubmatch(text); len(match) == 3 {
			manifest.Models[i].Size = match[1]
			manifest.Models[i].Checksum = match[2]
			manifest.Models[i].ChecksumType = "sha1"
		}
	}
	manifest.LastVerifiedAt = time.Now().UTC().Format("2006-01-02")
	return manifest, nil
}

func ensureHome(home string) error {
	for _, dir := range []string{"bin", "models/whisper", "downloads", "locks", "logs", "tmp", "cache/transcripts"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			return err
		}
	}
	test := filepath.Join(home, "tmp", ".write-test")
	if err := os.WriteFile(test, []byte("ok"), 0o644); err != nil {
		return err
	}
	_ = os.Remove(test)
	return nil
}

func autoclipHome(override string) string {
	if override != "" {
		return expandHome(override)
	}
	if env := os.Getenv("AUTOCLIP_HOME"); env != "" {
		return expandHome(env)
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "autoclip")
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "AutoClip")
		}
		return filepath.Join(home, "AppData", "Local", "AutoClip")
	default:
		return filepath.Join(home, ".local", "share", "autoclip")
	}
}

func expandHome(path string) string {
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func readInstallLock(home string) (InstallLock, error) {
	var lock InstallLock
	err := readJSONFile(filepath.Join(home, "dependencies.lock.json"), &lock)
	return lock, err
}

func writeInstallLock(home string, lock InstallLock) error {
	return writeJSONFile(filepath.Join(home, "dependencies.lock.json"), lock)
}

func discoverFFmpeg(home string) (string, string) {
	return discoverExecutable(home, "ffmpeg"), discoverExecutable(home, "ffprobe")
}

func discoverExecutable(home, name string) string {
	candidates := []string{filepath.Join(home, "bin", exeName(name))}
	if p, err := exec.LookPath(name); err == nil {
		candidates = append([]string{p}, candidates...)
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func exeName(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(name, ".exe") {
		return name + ".exe"
	}
	return name
}

func ensureWhisperCLI(home string, manifest DependencyManifest, opts GlobalOptions, repair bool, stdout, stderr io.Writer) (string, error) {
	if !repair {
		if p := discoverExecutable(home, "whisper-cli"); p != "" {
			fmt.Fprintf(stdout, "已找到 whisper-cli：%s\n", p)
			return p, nil
		}
	}
	if manifest.WhisperCPP.SourceURL == "" || manifest.WhisperCPP.Checksum == "" {
		return "", errors.New("manifest 未提供可校验的 whisper.cpp 源码包")
	}
	srcArchive := filepath.Join(home, "downloads", "whisper.cpp-"+manifest.WhisperCPP.Version+".tar.gz")
	if err := downloadAndVerifyAny(downloadSources(manifest.WhisperCPP.SourceURL, manifest.WhisperCPP.MirrorURLs), srcArchive, manifest.WhisperCPP.ChecksumType, manifest.WhisperCPP.Checksum, stdout); err != nil {
		return "", err
	}
	srcDir := filepath.Join(home, "downloads", "whisper.cpp-"+manifest.WhisperCPP.Version)
	_ = os.RemoveAll(srcDir)
	if err := extractTarGz(srcArchive, srcDir); err != nil {
		return "", err
	}
	root, err := firstDir(srcDir)
	if err != nil {
		return "", err
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		return "", errors.New("未找到 make，无法从源码构建 whisper.cpp；请安装 Xcode Command Line Tools 或放置 whisper-cli 到 AUTOCLIP_HOME/bin")
	}
	cmakePath, err := ensureCMake(home, stdout)
	if err != nil {
		return "", err
	}
	jobs := strconv.Itoa(maxInt(1, runtime.NumCPU()-1))
	cmd := exec.Command(makePath, "-j"+jobs)
	cmd.Dir = root
	cmd.Env = withPrependedPath(os.Environ(), filepath.Dir(cmakePath))
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("构建 whisper.cpp 失败：%w", err)
	}
	for _, rel := range []string{
		filepath.Join("build", "bin", exeName("whisper-cli")),
		filepath.Join("build", "bin", exeName("main")),
		exeName("whisper-cli"),
		exeName("main"),
	} {
		candidate := filepath.Join(root, rel)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			dst := filepath.Join(home, "bin", exeName("whisper-cli"))
			if err := copyFile(candidate, dst, 0o755); err != nil {
				return "", err
			}
			return dst, nil
		}
	}
	return "", errors.New("构建完成但未找到 whisper-cli 可执行文件")
}

func ensureCMake(home string, stdout io.Writer) (string, error) {
	if p := discoverCMake(home); p != "" {
		return p, nil
	}
	if runtime.GOOS != "darwin" {
		return "", errors.New("未找到 cmake，当前平台暂未内置 CMake 下载；请将 cmake 放入 PATH 后重试")
	}
	const version = "4.3.2"
	const checksum = "808ab43a0db04c8eec9ed7db12b90d7be1c8e2e75f4a060724d604a2043ccaf7"
	url := "https://github.com/Kitware/CMake/releases/download/v" + version + "/cmake-" + version + "-macos-universal.tar.gz"
	mirrors := []string{"https://cmake.org/files/v4.3/cmake-" + version + "-macos-universal.tar.gz"}
	archive := filepath.Join(home, "downloads", "cmake-"+version+"-macos-universal.tar.gz")
	if err := downloadAndVerifyAny(downloadSources(url, mirrors), archive, "sha256", checksum, stdout); err != nil {
		return "", fmt.Errorf("CMake 下载失败：%w", err)
	}
	toolsDir := filepath.Join(home, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		return "", err
	}
	if err := extractTarGz(archive, toolsDir); err != nil {
		return "", fmt.Errorf("CMake 解压失败：%w", err)
	}
	if p := discoverCMake(home); p != "" {
		return p, nil
	}
	return "", errors.New("CMake 已下载但未找到可执行文件")
}

func discoverCMake(home string) string {
	candidates := []string{
		filepath.Join(home, "tools", "cmake-4.3.2-macos-universal", "CMake.app", "Contents", "bin", "cmake"),
		filepath.Join(home, "bin", exeName("cmake")),
	}
	if p, err := exec.LookPath("cmake"); err == nil {
		candidates = append([]string{p}, candidates...)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func withPrependedPath(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	pathValue := dir + string(os.PathListSeparator) + os.Getenv("PATH")
	var out []string
	replaced := false
	for _, item := range env {
		if strings.HasPrefix(item, "PATH=") {
			out = append(out, "PATH="+pathValue)
			replaced = true
		} else {
			out = append(out, item)
		}
	}
	if !replaced {
		out = append(out, "PATH="+pathValue)
	}
	return out
}

func ensureModel(home string, model ModelEntry, repair bool, stdout io.Writer) (InstalledDependency, error) {
	dst := filepath.Join(home, "models", "whisper", model.FileName)
	if !repair {
		if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
			if err := verifyFile(dst, model.ChecksumType, model.Checksum); err == nil {
				fmt.Fprintf(stdout, "模型已存在并通过校验：%s\n", dst)
				return installedModel(dst, model), nil
			}
		}
	}
	if err := downloadAndVerifyAny(downloadSources(model.URL, model.MirrorURLs), dst, model.ChecksumType, model.Checksum, stdout); err != nil {
		return InstalledDependency{}, err
	}
	return installedModel(dst, model), nil
}

func ensureBinaryFromManifest(home string, manifest DependencyManifest, name string, repair bool, stdout io.Writer) (InstalledDependency, error) {
	entry, ok := matchingBinary(manifest, name)
	if !ok {
		return InstalledDependency{}, errors.New("没有匹配 " + runtime.GOOS + "/" + runtime.GOARCH + " 的 " + name + " 二进制")
	}
	dst := filepath.Join(home, "bin", exeName(name))
	if !repair {
		if st, err := os.Stat(dst); err == nil && !st.IsDir() {
			return InstalledDependency{Name: name, Path: dst, Source: entry.URL, ChecksumType: entry.ChecksumType, Checksum: entry.Checksum}, nil
		}
	}
	if entry.Checksum == "" {
		return InstalledDependency{}, errors.New("拒绝下载未提供 checksum 的 " + name)
	}
	archive := filepath.Join(home, "downloads", name+"-"+runtime.GOOS+"-"+runtime.GOARCH)
	if entry.ArchiveType != "" {
		archive += "." + strings.TrimPrefix(entry.ArchiveType, ".")
	}
	if err := downloadAndVerifyAny(downloadSources(entry.URL, entry.MirrorURLs), archive, entry.ChecksumType, entry.Checksum, stdout); err != nil {
		return InstalledDependency{}, err
	}
	switch entry.ArchiveType {
	case "zip":
		if err := extractZipBinary(archive, entry.PathInArchive, dst); err != nil {
			return InstalledDependency{}, err
		}
	case "", "raw":
		if err := copyFile(archive, dst, 0o755); err != nil {
			return InstalledDependency{}, err
		}
	default:
		return InstalledDependency{}, fmt.Errorf("不支持的二进制 archive_type：%s", entry.ArchiveType)
	}
	return InstalledDependency{Name: name, Path: dst, Source: entry.URL, ChecksumType: entry.ChecksumType, Checksum: entry.Checksum}, nil
}

func matchingBinary(manifest DependencyManifest, name string) (BinaryEntry, bool) {
	for _, entry := range manifest.Binaries {
		if entry.Name == name && entry.OS == runtime.GOOS && entry.Arch == runtime.GOARCH {
			return entry, true
		}
	}
	return BinaryEntry{}, false
}

func installedModel(path string, model ModelEntry) InstalledDependency {
	return InstalledDependency{Name: model.Name, Path: path, Source: model.URL, Size: model.Size, SizeBytes: model.SizeBytes, ChecksumType: model.ChecksumType, Checksum: model.Checksum}
}

func downloadSources(primary string, mirrors []string) []string {
	seen := map[string]bool{}
	var urls []string
	for _, raw := range append([]string{primary}, mirrors...) {
		u := strings.TrimSpace(raw)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		urls = append(urls, u)
	}
	return urls
}

func downloadAndVerify(url, dst, checksumType, checksum string, stdout io.Writer) error {
	return downloadAndVerifyAny(downloadSources(url, nil), dst, checksumType, checksum, stdout)
}

func downloadAndVerifyAny(urls []string, dst, checksumType, checksum string, stdout io.Writer) error {
	if checksum == "" {
		return errors.New("拒绝下载未提供 checksum 的依赖")
	}
	if len(urls) == 0 {
		return errors.New("manifest 未提供下载来源")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		if err := verifyFile(dst, checksumType, checksum); err == nil {
			fmt.Fprintf(stdout, "文件已存在并通过校验：%s\n", dst)
			return nil
		}
		fmt.Fprintf(stdout, "已有文件校验未通过，将重新下载：%s\n", dst)
		_ = os.Remove(dst)
	}
	tmp := dst + ".download"
	var failures []string
	for i, url := range urls {
		label := "官方源"
		if i > 0 {
			label = fmt.Sprintf("备用镜像 %d", i)
		}
		fmt.Fprintf(stdout, "下载%s：%s\n", label, url)
		if err := downloadOne(url, tmp, stdout); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", url, err))
			fmt.Fprintln(stdout, "当前来源下载失败，将尝试下一个来源。")
			continue
		}
		fmt.Fprintln(stdout, "校验 checksum...")
		if err := verifyFile(tmp, checksumType, checksum); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", url, err))
			_ = os.Remove(tmp)
			fmt.Fprintln(stdout, "当前来源校验失败，已丢弃临时文件，将尝试下一个来源。")
			continue
		}
		if err := os.Rename(tmp, dst); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("所有下载来源均失败：%s", strings.Join(failures, "；"))
}

func downloadOne(url, tmp string, stdout io.Writer) error {
	offset := int64(0)
	if st, err := os.Stat(tmp); err == nil && st.Size() > 0 {
		offset = st.Size()
		fmt.Fprintf(stdout, "检测到未完成下载，尝试从 %.1f MiB 续传。\n", float64(offset)/(1024*1024))
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0 {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if offset > 0 && resp.StatusCode == http.StatusPartialContent {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	} else if offset > 0 {
		fmt.Fprintln(stdout, "当前来源不支持续传，改为从头下载。")
	}
	out, err := os.OpenFile(tmp, flags, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return nil
}

func verifyFile(path, checksumType, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var actual string
	switch strings.ToLower(checksumType) {
	case "sha1":
		h := sha1.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		actual = hex.EncodeToString(h.Sum(nil))
	case "sha256":
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		actual = hex.EncodeToString(h.Sum(nil))
	default:
		return fmt.Errorf("不支持的 checksum 类型：%s", checksumType)
	}
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum 不匹配：got %s want %s", actual, expected)
	}
	return nil
}

func probeMedia(input, home string) (MediaInfo, error) {
	abs, _ := filepath.Abs(input)
	if _, err := os.Stat(abs); err != nil {
		return MediaInfo{}, err
	}
	ffprobe := discoverExecutable(home, "ffprobe")
	if ffprobe != "" {
		if info, err := probeWithFFprobe(ffprobe, abs); err == nil {
			return info, nil
		}
	}
	ffmpeg := discoverExecutable(home, "ffmpeg")
	if ffmpeg == "" {
		return MediaInfo{}, errors.New("未找到 ffprobe 或 ffmpeg")
	}
	return probeWithFFmpeg(ffmpeg, abs)
}

func probeWithFFprobe(ffprobe, input string) (MediaInfo, error) {
	cmd := exec.Command(ffprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", input)
	out, err := cmd.Output()
	if err != nil {
		return MediaInfo{}, err
	}
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			RFrameRate string `json:"r_frame_rate"`
			SampleRate string `json:"sample_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return MediaInfo{}, err
	}
	info := MediaInfo{Path: input, ProbeTool: ffprobe}
	info.DurationSeconds, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	for _, s := range raw.Streams {
		switch s.CodecType {
		case "video":
			if info.VideoCodec == "" {
				info.VideoCodec, info.Width, info.Height, info.FrameRate = s.CodecName, s.Width, s.Height, s.RFrameRate
			}
		case "audio":
			if info.AudioCodec == "" {
				info.AudioCodec = s.CodecName
				info.AudioSampleRate, _ = strconv.Atoi(s.SampleRate)
			}
		}
	}
	info.EstimatedSettings = "accurate 模式将重编码为 H.264/AAC"
	return info, nil
}

func probeWithFFmpeg(ffmpeg, input string) (MediaInfo, error) {
	cmd := exec.Command(ffmpeg, "-hide_banner", "-i", input)
	out, _ := cmd.CombinedOutput()
	text := string(out)
	info := MediaInfo{Path: input, ProbeTool: ffmpeg + " fallback", EstimatedSettings: "accurate 模式将重编码为 H.264/AAC"}
	if m := regexp.MustCompile(`Duration:\s+(\d+):(\d+):(\d+\.\d+)`).FindStringSubmatch(text); len(m) == 4 {
		h, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		sec, _ := strconv.ParseFloat(m[3], 64)
		info.DurationSeconds = float64(h*3600+min*60) + sec
	}
	if m := regexp.MustCompile(`Video:\s+([^,\s]+).*?(\d{2,5})x(\d{2,5})`).FindStringSubmatch(text); len(m) == 4 {
		info.VideoCodec = m[1]
		info.Width, _ = strconv.Atoi(m[2])
		info.Height, _ = strconv.Atoi(m[3])
	}
	if m := regexp.MustCompile(`Audio:\s+([^,\s]+).*?(\d+)\s+Hz`).FindStringSubmatch(text); len(m) == 3 {
		info.AudioCodec = m[1]
		info.AudioSampleRate, _ = strconv.Atoi(m[2])
	}
	if info.DurationSeconds <= 0 {
		return info, errors.New("无法从 FFmpeg 输出解析时长")
	}
	return info, nil
}

var errCloudReserved = errors.New("云端转写接口已预留，但本版本未启用；请使用 --provider local")

func transcribeLocal(p ProcessOptions, opts GlobalOptions, paths OutputPaths, stdout io.Writer) (Transcript, error) {
	if p.Provider == "" || p.Provider == "auto" {
		p.Provider = "local"
	}
	if p.Provider != "local" {
		return Transcript{}, errCloudReserved
	}
	home := autoclipHome(opts.Home)
	whisper := discoverExecutable(home, "whisper-cli")
	if whisper == "" {
		return Transcript{}, errors.New("未找到 whisper-cli，请先运行 autoclip install --profile <档位>")
	}
	modelPath, modelName, err := resolveModelPath(home, p.Model)
	if err != nil {
		return Transcript{}, err
	}
	ffmpeg := discoverExecutable(home, "ffmpeg")
	if ffmpeg == "" {
		return Transcript{}, errors.New("未找到 ffmpeg")
	}
	if err := os.MkdirAll(filepath.Dir(paths.Audio), 0o755); err != nil {
		return Transcript{}, err
	}
	cmd := exec.Command(ffmpeg, "-y", "-i", p.Input, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", paths.Audio)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Transcript{}, fmt.Errorf("音频提取失败：%w\n%s", err, string(out))
	}
	base := strings.TrimSuffix(paths.TranscriptJSON, ".json")
	args := []string{"-m", modelPath, "-f", paths.Audio, "-oj", "-osrt", "-of", base}
	if p.Timestamps == "word" {
		args = append(args, "-ojf")
	}
	if p.Language != "" && p.Language != "auto" {
		args = append(args, "-l", p.Language)
	}
	cmd = exec.Command(whisper, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Transcript{}, fmt.Errorf("whisper.cpp 转写失败：%w\n%s", err, string(out))
	}
	if !p.KeepAudio {
		defer os.Remove(paths.Audio)
	}
	tr, err := readWhisperJSON(paths.TranscriptJSON, p.Language)
	if err != nil {
		return Transcript{}, err
	}
	if tr.Metadata == nil {
		tr.Metadata = map[string]string{}
	}
	tr.Metadata["provider"] = "local"
	tr.Metadata["model"] = modelName
	return tr, nil
}

func resolveModelPath(home, model string) (string, string, error) {
	if model != "" {
		if st, err := os.Stat(model); err == nil && !st.IsDir() {
			return model, filepath.Base(model), nil
		}
		manifest := loadDependencyManifest()
		for _, m := range manifest.Models {
			if model == m.Name || model == m.Profile {
				path := filepath.Join(home, "models", "whisper", m.FileName)
				if _, err := os.Stat(path); err != nil {
					return "", "", fmt.Errorf("模型 %s 尚未安装，请运行 autoclip install --profile %s", m.Name, m.Profile)
				}
				return path, m.Name, nil
			}
		}
		return "", "", fmt.Errorf("未知模型：%s", model)
	}
	lock, err := readInstallLock(home)
	if err == nil && lock.Model.Path != "" {
		if _, statErr := os.Stat(lock.Model.Path); statErr == nil {
			return lock.Model.Path, lock.Model.Name, nil
		}
	}
	return "", "", errors.New("未安装本地模型，请先运行 autoclip install --profile lite|standard|fast-high|best")
}

func readTranscriptFile(path, fallbackLanguage string) (Transcript, error) {
	var tr Transcript
	if err := readJSONFile(path, &tr); err == nil && len(tr.Segments) > 0 {
		if tr.SchemaVersion == "" {
			tr.SchemaVersion = schemaVersion
		}
		if tr.Language == "" {
			tr.Language = valueOr(fallbackLanguage, "auto")
		}
		normalizeTranscriptSegments(&tr)
		return tr, nil
	}
	tr, err := readWhisperJSON(path, fallbackLanguage)
	if err != nil {
		return tr, err
	}
	normalizeTranscriptSegments(&tr)
	return tr, nil
}

func normalizeTranscriptSegments(tr *Transcript) {
	for i := range tr.Segments {
		if tr.Segments[i].ID == 0 && i != 0 {
			tr.Segments[i].ID = i
		}
		tr.Segments[i].Start = round3(tr.Segments[i].Start)
		tr.Segments[i].End = round3(tr.Segments[i].End)
		tr.Segments[i].Text = strings.TrimSpace(tr.Segments[i].Text)
		for j := range tr.Segments[i].Words {
			tr.Segments[i].Words[j].Start = round3(tr.Segments[i].Words[j].Start)
			tr.Segments[i].Words[j].End = round3(tr.Segments[i].Words[j].End)
			tr.Segments[i].Words[j].Text = strings.TrimSpace(tr.Segments[i].Words[j].Text)
		}
	}
}

func readWhisperJSON(path, language string) (Transcript, error) {
	var raw interface{}
	if err := readJSONFile(path, &raw); err != nil {
		return Transcript{}, err
	}
	tr := Transcript{SchemaVersion: schemaVersion, Language: language}
	if language == "" {
		tr.Language = "auto"
	}
	switch v := raw.(type) {
	case map[string]interface{}:
		if lang, ok := v["language"].(string); ok && lang != "" {
			tr.Language = lang
		}
		if arr, ok := v["transcription"].([]interface{}); ok {
			tr.Segments = parseWhisperSegments(arr)
		} else if arr, ok := v["segments"].([]interface{}); ok {
			tr.Segments = parseWhisperSegments(arr)
		}
	}
	if len(tr.Segments) == 0 {
		return tr, errors.New("未能从 whisper JSON 中解析任何转写片段")
	}
	return tr, nil
}

func parseWhisperSegments(arr []interface{}) []TranscriptSeg {
	var out []TranscriptSeg
	for i, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		seg := TranscriptSeg{ID: i, Text: stringFromAny(m["text"])}
		seg.Start = segmentSeconds(m, []string{"start", "from", "t0"}, "from")
		seg.End = segmentSeconds(m, []string{"end", "to", "t1"}, "to")
		if words, ok := m["words"].([]interface{}); ok {
			for _, wv := range words {
				wm, ok := wv.(map[string]interface{})
				if !ok {
					continue
				}
				seg.Words = append(seg.Words, TranscriptWord{
					Start:      segmentSeconds(wm, []string{"start", "from", "t0"}, "from"),
					End:        segmentSeconds(wm, []string{"end", "to", "t1"}, "to"),
					Text:       stringFromAny(firstPresent(wm, "text", "word")),
					Confidence: floatFromAny(firstPresent(wm, "confidence", "probability", "p")),
				})
			}
		}
		if len(seg.Words) == 0 {
			if tokens, ok := m["tokens"].([]interface{}); ok {
				seg.Words = parseWhisperTokens(tokens)
			}
		}
		if seg.End > seg.Start || seg.Text != "" {
			out = append(out, seg)
		}
	}
	return out
}

func parseWhisperTokens(arr []interface{}) []TranscriptWord {
	var out []TranscriptWord
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		text := strings.TrimSpace(stringFromAny(m["text"]))
		if !usableWordToken(text) {
			continue
		}
		start := segmentSeconds(m, []string{"start", "from", "t0"}, "from")
		end := segmentSeconds(m, []string{"end", "to", "t1"}, "to")
		if end <= start {
			continue
		}
		out = append(out, TranscriptWord{
			Start:      round3(start),
			End:        round3(end),
			Text:       text,
			Confidence: floatFromAny(firstPresent(m, "confidence", "probability", "p")),
		})
	}
	return out
}

func usableWordToken(text string) bool {
	if text == "" || strings.HasPrefix(text, "[") || strings.Contains(text, "�") {
		return false
	}
	return normalizePhrase(text) != ""
}

func segmentSeconds(m map[string]interface{}, directKeys []string, nestedKey string) float64 {
	if v := firstPresent(m, directKeys...); v != nil {
		return secondsFromAny(v)
	}
	if offsets, ok := m["offsets"].(map[string]interface{}); ok {
		if v, exists := offsets[nestedKey]; exists {
			return floatFromAny(v) / 1000
		}
	}
	if timestamps, ok := m["timestamps"].(map[string]interface{}); ok {
		if v, exists := timestamps[nestedKey]; exists {
			return secondsFromAny(v)
		}
	}
	return 0
}

func processOne(p ProcessOptions, opts GlobalOptions, render bool, stdout io.Writer) (map[string]interface{}, error) {
	info, err := probeMedia(p.Input, autoclipHome(opts.Home))
	if err != nil {
		return nil, err
	}
	paths := makeOutputPaths(p.Input, p.Out, render, p.RenderMode)
	if err := os.MkdirAll(filepath.Dir(paths.Cuts), 0o755); err != nil {
		return nil, err
	}
	if render && p.Resume && outputsComplete(paths) {
		return map[string]interface{}{"ok": true, "skipped": true, "input": p.Input, "output": paths.Video, "cuts": paths.Cuts, "report": paths.Report, "manifest": paths.Manifest, "cut_count": 0, "removed_seconds": 0.0}, nil
	}
	var transcript Transcript
	var transcriptSource string
	if p.Transcript != "" {
		transcript, err = readTranscriptFile(p.Transcript, p.Language)
		if err != nil {
			return nil, fmt.Errorf("读取指定 transcript 失败：%w", err)
		}
		if transcript.Metadata == nil {
			transcript.Metadata = map[string]string{}
		}
		transcript.Metadata["source_path"] = absPath(p.Transcript)
		transcriptSource = "provided"
	} else {
		cachePath := ""
		if p.UseCache {
			cachePath, _ = transcriptCachePath(autoclipHome(opts.Home), p)
			if cachePath != "" {
				if cached, cacheErr := readTranscriptFile(cachePath, p.Language); cacheErr == nil {
					transcript = cached
					if transcript.Metadata == nil {
						transcript.Metadata = map[string]string{}
					}
					transcript.Metadata["cache_hit"] = "true"
					transcript.Metadata["cache_path"] = cachePath
					transcriptSource = "cache"
				}
			}
		}
		if len(transcript.Segments) == 0 {
			transcript, err = transcribeLocal(p, opts, paths, stdout)
			if err != nil {
				return nil, err
			}
			transcriptSource = "local_whisper"
			if cachePath != "" {
				_ = writeJSONFile(cachePath, transcript)
			}
		}
	}
	if err := writeJSONFile(paths.TranscriptJSON, transcript); err != nil {
		return nil, err
	}
	if err := writeSRT(paths.TranscriptSRT, transcript); err != nil {
		return nil, err
	}
	if err := writeASS(paths.TranscriptASS, transcript, p.SubtitleStyle); err != nil {
		return nil, err
	}
	candidates := detectCandidates(p, transcript, info, autoclipHome(opts.Home))
	plan, err := planCuts(p, info.DurationSeconds, candidates)
	if err != nil {
		return nil, err
	}
	if err := writeJSONFile(paths.Cuts, plan); err != nil {
		return nil, err
	}
	if err := writeCandidatePreviewMD(paths.PreviewMD, plan); err != nil {
		return nil, err
	}
	if err := writeCandidatePreviewHTML(paths.PreviewHTML, plan); err != nil {
		return nil, err
	}
	outputDuration := durationKept(plan.KeptSegments)
	if render {
		if err := renderOutput(p, paths, plan, autoclipHome(opts.Home)); err != nil {
			return nil, err
		}
	} else {
		paths.Video = ""
	}
	manifest := buildRunManifest(p, paths, info, plan, outputDuration, opts)
	manifest.Config["transcript_source"] = transcriptSource
	if p.Transcript != "" {
		manifest.Config["transcript_input"] = absPath(p.Transcript)
	}
	if err := writeJSONFile(paths.Manifest, manifest); err != nil {
		return nil, err
	}
	if err := writeReport(paths.Report, p, info, plan, manifest); err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"ok": true, "input": p.Input, "output": paths.Video, "cuts": paths.Cuts,
		"report": paths.Report, "manifest": paths.Manifest, "cut_count": len(plan.Cuts),
		"preview": paths.PreviewHTML, "removed_seconds": round3(info.DurationSeconds - outputDuration),
	}, nil
}

func processVariants(p ProcessOptions, opts GlobalOptions, stdout io.Writer) (map[string]interface{}, error) {
	variants := splitList(p.Variants)
	if len(variants) == 0 {
		return nil, errors.New("未提供 variants")
	}
	for _, v := range variants {
		if _, err := presetByName(v); err != nil {
			return nil, err
		}
	}
	baseOutDir := variantOutputDir(p.Input, p.Out)
	if err := os.MkdirAll(baseOutDir, 0o755); err != nil {
		return nil, err
	}
	transcriptPaths := makeOutputPaths(p.Input, baseOutDir, false, p.RenderMode)
	var transcript Transcript
	var err error
	if p.Transcript != "" {
		transcript, err = readTranscriptFile(p.Transcript, p.Language)
	} else {
		transcript, err = transcribeLocal(p, opts, transcriptPaths, stdout)
	}
	if err != nil {
		return nil, err
	}
	if err := writeJSONFile(transcriptPaths.TranscriptJSON, transcript); err != nil {
		return nil, err
	}
	if err := writeSRT(transcriptPaths.TranscriptSRT, transcript); err != nil {
		return nil, err
	}
	if err := writeASS(transcriptPaths.TranscriptASS, transcript, p.SubtitleStyle); err != nil {
		return nil, err
	}
	var items []map[string]interface{}
	success := 0
	for _, preset := range variants {
		vp := p
		vp.Preset = preset
		vp.Variants = ""
		vp.Transcript = transcriptPaths.TranscriptJSON
		vp.Out = filepath.Join(baseOutDir, variantFileName(p.Input, preset, p.RenderMode))
		result, err := processOne(vp, opts, true, stdout)
		row := map[string]interface{}{"preset": preset}
		if err != nil {
			row["ok"] = false
			row["error"] = err.Error()
		} else {
			row["ok"] = true
			row["output"] = result["output"]
			row["report"] = result["report"]
			row["removed_seconds"] = result["removed_seconds"]
			success++
		}
		items = append(items, row)
	}
	return map[string]interface{}{"ok": success == len(variants), "total": len(variants), "success": success, "failed": len(variants) - success, "out_dir": baseOutDir, "transcript": transcriptPaths.TranscriptJSON, "items": items}, nil
}

func variantOutputDir(input, out string) string {
	if out != "" {
		out = expandHome(out)
		if filepath.Ext(out) == "" || isDir(out) {
			return out
		}
		return filepath.Dir(out)
	}
	return filepath.Join(filepath.Dir(absPath(input)), strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))+".autoclip-variants")
}

func variantFileName(input, preset, renderMode string) string {
	stem := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	ext := ".mp4"
	if renderMode == "audio-only" {
		ext = ".m4a"
	}
	return stem + "." + preset + ".autoclip" + ext
}

func transcriptCachePath(home string, p ProcessOptions) (string, error) {
	inputHash, err := fileSHA256(p.Input)
	if err != nil {
		return "", err
	}
	modelKey := p.Model
	if modelKey == "" {
		if lock, err := readInstallLock(home); err == nil && lock.Model.Name != "" {
			modelKey = lock.Model.Name
		}
	}
	key := strings.Join([]string{inputHash, valueOr(p.Language, "auto"), valueOr(modelKey, "default"), valueOr(p.Timestamps, "segment")}, "-")
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(home, "cache", "transcripts", hex.EncodeToString(sum[:])+".json"), nil
}

func outputsComplete(paths OutputPaths) bool {
	required := []string{paths.Video, paths.Cuts, paths.TranscriptJSON, paths.Report, paths.Manifest}
	for _, p := range required {
		if p == "" {
			return false
		}
		if st, err := os.Stat(p); err != nil || st.IsDir() || st.Size() == 0 {
			return false
		}
	}
	return true
}

func detectCandidates(p ProcessOptions, tr Transcript, info MediaInfo, home string) []CutCandidate {
	preset, _ := presetByName(p.Preset)
	if p.MinSilence > 0 {
		preset.MinSilenceSeconds = p.MinSilence
	}
	if p.PrePadMS >= 0 {
		preset.PrePadMS = p.PrePadMS
	}
	if p.PostPadMS >= 0 {
		preset.PostPadMS = p.PostPadMS
	}
	fillers := fillerSet(p.Language, p.FillerList)
	protected := protectSet(p.Language, p.ProtectList)
	var candidates []CutCandidate
	nextID := func() string { return fmt.Sprintf("cut_%05d", len(candidates)+1) }
	for _, seg := range tr.Segments {
		if len(seg.Words) > 0 {
			candidates = append(candidates, detectWordFillers(seg, fillers, protected, preset, nextID)...)
			continue
		}
		normalized := normalizePhrase(seg.Text)
		if protected[normalized] {
			candidates = append(candidates, reviewCandidate(nextID(), "filler", seg.Start, seg.End, seg.Text, 0, "protected segment", preset, true))
		} else if fillers[normalized] && seg.End-seg.Start <= preset.MaxCutSecondsWithoutConfirmation {
			candidates = append(candidates, removeCandidate(nextID(), "filler", seg.Start, seg.End, seg.Text, 0.8, "transcript_segment", "filler-only segment", preset))
		} else if containsConfiguredFiller(normalized, fillers) {
			candidates = append(candidates, reviewCandidate(nextID(), "filler", seg.Start, seg.End, seg.Text, 0, "segment timestamp too broad", preset, false))
		}
	}
	for _, s := range detectSilences(home, p.Input, preset.MinSilenceSeconds) {
		if s.End > s.Start {
			typ := "silence"
			reason := "long silence"
			if p.TrimEdges && isEdgeSilence(s, info.DurationSeconds) {
				typ = "edge_silence"
				reason = "leading/trailing silence"
			}
			candidates = append(candidates, removeCandidate(nextID(), typ, s.Start, s.End, "", 1, "ffmpeg_silencedetect", reason, preset))
		}
	}
	return candidates
}

func detectWordFillers(seg TranscriptSeg, fillers, protected map[string]bool, preset Preset, nextID func() string) []CutCandidate {
	var candidates []CutCandidate
	used := map[int]bool{}
	maxWindow := 4
	for i := 0; i < len(seg.Words); i++ {
		if used[i] {
			continue
		}
		bestEnd := -1
		bestText := ""
		bestNorm := ""
		bestConfidence := 0.0
		for j := i; j < len(seg.Words) && j < i+maxWindow; j++ {
			joined := joinWordTexts(seg.Words[i : j+1])
			norm := normalizePhrase(joined)
			compact := compactPhrase(norm)
			if protected[norm] || protected[compact] {
				candidates = append(candidates, reviewCandidate(nextID(), "filler", seg.Words[i].Start, seg.Words[j].End, joined, averageWordConfidence(seg.Words[i:j+1]), "protected word", preset, true))
				break
			}
			if fillers[norm] || fillers[compact] {
				bestEnd = j
				bestText = joined
				bestNorm = compact
				bestConfidence = averageWordConfidence(seg.Words[i : j+1])
			}
		}
		if bestEnd < i {
			continue
		}
		words := seg.Words[i : bestEnd+1]
		if confidenceOK(bestConfidence, preset.MinFillerConfidence) && safeWordCutBoundary(seg.Words, i, bestEnd, seg, bestNorm) {
			candidates = append(candidates, removeCandidate(nextID(), "filler", words[0].Start, words[len(words)-1].End, bestText, bestConfidence, "transcript_word", "standalone low-risk filler word", preset))
			for k := i; k <= bestEnd; k++ {
				used[k] = true
			}
		} else {
			candidates = append(candidates, reviewCandidate(nextID(), "filler", words[0].Start, words[len(words)-1].End, bestText, bestConfidence, "word timestamp boundary not isolated enough", preset, false))
		}
	}
	return candidates
}

func safeWordCutBoundary(words []TranscriptWord, startIdx, endIdx int, seg TranscriptSeg, text string) bool {
	if startIdx < 0 || endIdx >= len(words) || startIdx > endIdx {
		return false
	}
	start := words[startIdx].Start
	end := words[endIdx].End
	if end <= start || end-start > 1.2 {
		return false
	}
	beforeGap := start - seg.Start
	afterGap := seg.End - end
	if startIdx > 0 {
		beforeGap = start - words[startIdx-1].End
	}
	if endIdx+1 < len(words) {
		afterGap = words[endIdx+1].Start - end
	}
	shortInterjection := map[string]bool{"嗯": true, "呃": true, "啊": true, "额": true, "唔": true, "呐": true}
	threshold := 0.12
	if shortInterjection[text] {
		threshold = 0.08
		return beforeGap >= threshold || afterGap >= threshold || startIdx == 0 || endIdx == len(words)-1
	}
	isWholeSegment := startIdx == 0 && endIdx == len(words)-1
	return isWholeSegment || (beforeGap >= threshold && afterGap >= threshold)
}

func joinWordTexts(words []TranscriptWord) string {
	var b strings.Builder
	for _, w := range words {
		b.WriteString(strings.TrimSpace(w.Text))
	}
	return b.String()
}

func compactPhrase(s string) string {
	return strings.ReplaceAll(s, " ", "")
}

func averageWordConfidence(words []TranscriptWord) float64 {
	if len(words) == 0 {
		return 0
	}
	total := 0.0
	count := 0
	for _, w := range words {
		if w.Confidence > 0 {
			total += w.Confidence
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func isEdgeSilence(s TimeSegment, duration float64) bool {
	return s.Start <= 0.15 || (duration > 0 && duration-s.End <= 0.15)
}

func removeCandidate(id, typ string, start, end float64, text string, conf float64, source, reason string, preset Preset) CutCandidate {
	return CutCandidate{ID: id, Type: typ, Start: round3(start), End: round3(end), Text: text, Confidence: conf, Source: source, Decision: "remove", Reason: reason, Safety: CutSafety{PrePadMS: preset.PrePadMS, PostPadMS: preset.PostPadMS}}
}

func reviewCandidate(id, typ string, start, end float64, text string, conf float64, reason string, preset Preset, protected bool) CutCandidate {
	return CutCandidate{ID: id, Type: typ, Start: round3(start), End: round3(end), Text: text, Confidence: conf, Source: "transcript", Decision: "review", Reason: reason, Safety: CutSafety{PrePadMS: preset.PrePadMS, PostPadMS: preset.PostPadMS, ProtectedContext: protected}}
}

func detectSilences(home, input string, minSilence float64) []TimeSegment {
	ffmpeg := discoverExecutable(home, "ffmpeg")
	if ffmpeg == "" {
		return nil
	}
	cmd := exec.Command(ffmpeg, "-hide_banner", "-i", input, "-af", fmt.Sprintf("silencedetect=noise=-35dB:d=%.3f", minSilence), "-f", "null", "-")
	out, _ := cmd.CombinedOutput()
	lines := strings.Split(string(out), "\n")
	var silences []TimeSegment
	var current *TimeSegment
	reStart := regexp.MustCompile(`silence_start:\s*([0-9.]+)`)
	reEnd := regexp.MustCompile(`silence_end:\s*([0-9.]+)`)
	for _, line := range lines {
		if m := reStart.FindStringSubmatch(line); len(m) == 2 {
			v, _ := strconv.ParseFloat(m[1], 64)
			current = &TimeSegment{Start: v}
		}
		if m := reEnd.FindStringSubmatch(line); len(m) == 2 && current != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			current.End = v
			silences = append(silences, *current)
			current = nil
		}
	}
	return silences
}

func planCuts(p ProcessOptions, duration float64, candidates []CutCandidate) (CutPlan, error) {
	preset, err := presetByName(p.Preset)
	if err != nil {
		return CutPlan{}, err
	}
	if p.PrePadMS >= 0 {
		preset.PrePadMS = p.PrePadMS
	}
	if p.PostPadMS >= 0 {
		preset.PostPadMS = p.PostPadMS
	}
	var cuts []PlannedCut
	edge := float64(preset.EdgeGuardMS) / 1000
	for _, c := range candidates {
		if c.Decision != "remove" {
			continue
		}
		start := math.Max(0, c.Start-float64(preset.PrePadMS)/1000)
		end := math.Min(duration, c.End+float64(preset.PostPadMS)/1000)
		if c.Type == "edge_silence" {
			if c.Start <= 0.15 {
				start = 0
			}
			if duration-c.End <= 0.15 {
				end = duration
			}
		}
		if c.Type != "edge_silence" && (start < edge || end > duration-edge) {
			continue
		}
		if end <= start {
			continue
		}
		cuts = append(cuts, PlannedCut{ID: c.ID, Start: round3(start), End: round3(end), Type: c.Type, Label: c.Text})
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].Start < cuts[j].Start })
	merged := mergeCuts(cuts, float64(preset.MergeGapMS)/1000)
	kept := keptSegments(duration, merged, float64(preset.MinKeptSegmentMS)/1000)
	return CutPlan{SchemaVersion: schemaVersion, InputDurationSeconds: duration, Preset: preset.Name, Cuts: merged, KeptSegments: kept, Candidates: candidates}, nil
}

func mergeCuts(cuts []PlannedCut, gap float64) []PlannedCut {
	if len(cuts) == 0 {
		return nil
	}
	out := []PlannedCut{cuts[0]}
	for _, c := range cuts[1:] {
		last := &out[len(out)-1]
		if c.Start-last.End <= gap {
			if c.End > last.End {
				last.End = c.End
			}
			if last.Label == "" {
				last.Label = c.Label
			}
			if !strings.Contains(last.Type, c.Type) {
				last.Type += "+" + c.Type
			}
		} else {
			out = append(out, c)
		}
	}
	for i := range out {
		out[i].Start = round3(out[i].Start)
		out[i].End = round3(out[i].End)
	}
	return out
}

func keptSegments(duration float64, cuts []PlannedCut, minKept float64) []TimeSegment {
	var kept []TimeSegment
	cursor := 0.0
	for _, c := range cuts {
		if c.Start-cursor >= minKept {
			kept = append(kept, TimeSegment{Start: round3(cursor), End: round3(c.Start)})
		}
		if c.End > cursor {
			cursor = c.End
		}
	}
	if duration-cursor >= minKept {
		kept = append(kept, TimeSegment{Start: round3(cursor), End: round3(duration)})
	}
	if len(kept) == 0 && duration > 0 {
		kept = append(kept, TimeSegment{Start: 0, End: round3(duration)})
	}
	return kept
}

func renderOutput(p ProcessOptions, paths OutputPaths, plan CutPlan, home string) error {
	if p.RenderMode == "" {
		p.RenderMode = "accurate"
	}
	if p.RenderMode != "accurate" && p.RenderMode != "audio-only" {
		return fmt.Errorf("render-mode %s 已预留但本版本未启用，请使用 accurate 或 audio-only", p.RenderMode)
	}
	if p.BurnSubtitles && p.RenderMode == "audio-only" {
		return errors.New("audio-only 模式不能烧录字幕")
	}
	ffmpeg := discoverExecutable(home, "ffmpeg")
	if ffmpeg == "" {
		return errors.New("未找到 ffmpeg")
	}
	if len(plan.KeptSegments) == 0 {
		return errors.New("剪辑方案没有可保留片段")
	}
	if err := os.MkdirAll(filepath.Dir(paths.Video), 0o755); err != nil {
		return err
	}
	subtitleFilter := ""
	if p.BurnSubtitles {
		subtitleFilter = "subtitles=" + escapeFilterPath(paths.TranscriptASS)
	}
	graph, maps := filterGraph(plan.KeptSegments, p.RenderMode == "audio-only", audioEnhanceFilter(p), subtitleFilter)
	args := []string{"-y", "-i", p.Input, "-filter_complex", graph}
	args = append(args, maps...)
	if p.RenderMode == "audio-only" {
		args = append(args, "-c:a", "aac", paths.Video)
	} else {
		args = append(args, "-c:v", "libx264", "-preset", "medium", "-crf", "20", "-c:a", "aac", paths.Video)
	}
	cmd := exec.Command(ffmpeg, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, string(out))
	}
	return nil
}

func filterGraph(segments []TimeSegment, audioOnly bool, audioFilter, videoFilter string) (string, []string) {
	var b strings.Builder
	for i, s := range segments {
		if !audioOnly {
			fmt.Fprintf(&b, "[0:v]trim=start=%.3f:end=%.3f,setpts=PTS-STARTPTS[v%d];", s.Start, s.End, i)
		}
		fmt.Fprintf(&b, "[0:a]atrim=start=%.3f:end=%.3f,asetpts=PTS-STARTPTS[a%d];", s.Start, s.End, i)
	}
	if audioOnly {
		for i := range segments {
			fmt.Fprintf(&b, "[a%d]", i)
		}
		fmt.Fprintf(&b, "concat=n=%d:v=0:a=1[acat];", len(segments))
		if audioFilter != "" {
			fmt.Fprintf(&b, "[acat]%s[outa]", audioFilter)
		} else {
			fmt.Fprintf(&b, "[acat]anull[outa]")
		}
		return b.String(), []string{"-map", "[outa]"}
	}
	for i := range segments {
		fmt.Fprintf(&b, "[v%d][a%d]", i, i)
	}
	fmt.Fprintf(&b, "concat=n=%d:v=1:a=1[vcat][acat];", len(segments))
	if videoFilter != "" {
		fmt.Fprintf(&b, "[vcat]%s[outv];", videoFilter)
	} else {
		fmt.Fprintf(&b, "[vcat]null[outv];")
	}
	if audioFilter != "" {
		fmt.Fprintf(&b, "[acat]%s[outa]", audioFilter)
	} else {
		fmt.Fprintf(&b, "[acat]anull[outa]")
	}
	return b.String(), []string{"-map", "[outv]", "-map", "[outa]"}
}

func audioEnhanceFilter(p ProcessOptions) string {
	var filters []string
	if p.AudioEnhance == "voice" || p.Denoise {
		filters = append(filters, "afftdn=nf=-25", "agate=threshold=0.02:ratio=2:attack=10:release=120")
	}
	if p.AudioEnhance == "voice" || p.Loudnorm {
		filters = append(filters, "loudnorm=I=-16:TP=-1.5:LRA=11")
	}
	return strings.Join(filters, ",")
}

func audioEnhanceSummary(p ProcessOptions) string {
	filter := audioEnhanceFilter(p)
	if filter == "" {
		return "none"
	}
	return filter
}

func escapeFilterPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "\\\\")
	path = strings.ReplaceAll(path, ":", "\\:")
	path = strings.ReplaceAll(path, "'", "\\'")
	path = strings.ReplaceAll(path, ",", "\\,")
	return "'" + path + "'"
}

func buildRunManifest(p ProcessOptions, paths OutputPaths, info MediaInfo, plan CutPlan, outputDuration float64, opts GlobalOptions) RunManifest {
	home := autoclipHome(opts.Home)
	lock, _ := readInstallLock(home)
	inputHash, _ := fileSHA256(p.Input)
	deps := map[string]interface{}{}
	if len(lock.Tools) > 0 {
		deps["tools"] = lock.Tools
	}
	if lock.Model.Name != "" {
		deps["model"] = lock.Model
		deps["profile"] = lock.Profile
	}
	return RunManifest{
		SchemaVersion: schemaVersion,
		RunID:         time.Now().UTC().Format("20060102-150405") + "-" + shortID(p.Input),
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Input:         ManifestInput{Path: absPath(p.Input), SHA256: inputHash},
		Outputs: map[string]string{
			"video": paths.Video, "cuts": paths.Cuts, "transcript_srt": paths.TranscriptSRT, "transcript_ass": paths.TranscriptASS,
			"transcript_json": paths.TranscriptJSON, "preview_md": paths.PreviewMD, "preview_html": paths.PreviewHTML,
			"report": paths.Report, "manifest": paths.Manifest,
		},
		Config: map[string]string{
			"preset": p.Preset, "language": valueOr(p.Language, "auto"), "render_mode": valueOr(p.RenderMode, "accurate"), "provider": "local",
			"timestamps": valueOr(p.Timestamps, "segment"), "audio_enhance": valueOr(p.AudioEnhance, "none"), "subtitle_style": valueOr(p.SubtitleStyle, "default"),
			"burn_subtitles": strconv.FormatBool(p.BurnSubtitles), "trim_edges": strconv.FormatBool(p.TrimEdges), "cache": strconv.FormatBool(p.UseCache),
		},
		Dependencies: deps,
		Stats:        ManifestStats{InputDurationSeconds: round3(info.DurationSeconds), OutputDurationSeconds: round3(outputDuration), RemovedDurationSeconds: round3(info.DurationSeconds - outputDuration), CutCount: len(plan.Cuts)},
	}
}

func writeReport(path string, p ProcessOptions, info MediaInfo, plan CutPlan, manifest RunManifest) error {
	var b strings.Builder
	fmt.Fprint(&b, "# AutoClip 报告\n\n")
	fmt.Fprintf(&b, "输入：%s\n\n", p.Input)
	if manifest.Outputs["video"] != "" {
		fmt.Fprintf(&b, "输出：%s\n\n", manifest.Outputs["video"])
	}
	fmt.Fprint(&b, "## 摘要\n\n")
	fmt.Fprintf(&b, "- 输入时长：%s\n", formatDuration(info.DurationSeconds))
	fmt.Fprintf(&b, "- 输出时长：%s\n", formatDuration(manifest.Stats.OutputDurationSeconds))
	fmt.Fprintf(&b, "- 移除：%s\n", formatDuration(manifest.Stats.RemovedDurationSeconds))
	fmt.Fprintf(&b, "- 剪辑点：%d\n", len(plan.Cuts))
	fmt.Fprintf(&b, "- 预设：%s\n", plan.Preset)
	fmt.Fprintf(&b, "- 转写来源：%s\n", manifest.Config["transcript_source"])
	if p.AudioEnhance != "" && p.AudioEnhance != "none" || p.Loudnorm || p.Denoise {
		fmt.Fprintf(&b, "- 音频增强：%s\n", audioEnhanceSummary(p))
	}
	if p.BurnSubtitles {
		fmt.Fprint(&b, "- 字幕：已烧录\n")
	}
	fmt.Fprint(&b, "\n")
	fmt.Fprint(&b, "## 剪辑分类\n\n")
	fmt.Fprintln(&b, "| 类型 | 数量 | 时长 |")
	fmt.Fprintln(&b, "|---|---:|---:|")
	for typ, stat := range cutBreakdown(plan.Cuts) {
		fmt.Fprintf(&b, "| %s | %d | %s |\n", typ, stat.Count, formatDuration(stat.Duration))
	}
	review, protected := reviewNotes(plan.Candidates)
	fmt.Fprint(&b, "\n## 复核提示\n\n")
	fmt.Fprintf(&b, "- 保留的候选项：%d\n", review)
	if len(protected) > 0 {
		fmt.Fprintf(&b, "- 保护词已保留：%s\n", strings.Join(protected, "、"))
	}
	fmt.Fprintf(&b, "- 候选预览：%s\n", manifest.Outputs["preview_md"])
	fmt.Fprint(&b, "\n## 文件\n\n")
	fmt.Fprintf(&b, "- 视频：%s\n", manifest.Outputs["video"])
	fmt.Fprintf(&b, "- 剪辑 JSON：%s\n", manifest.Outputs["cuts"])
	fmt.Fprintf(&b, "- 字幕 SRT：%s\n", manifest.Outputs["transcript_srt"])
	fmt.Fprintf(&b, "- 字幕 ASS：%s\n", manifest.Outputs["transcript_ass"])
	fmt.Fprintf(&b, "- 预览 HTML：%s\n", manifest.Outputs["preview_html"])
	fmt.Fprintf(&b, "- Manifest：%s\n", manifest.Outputs["manifest"])
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

type breakdownStat struct {
	Count    int
	Duration float64
}

func cutBreakdown(cuts []PlannedCut) map[string]breakdownStat {
	stats := map[string]breakdownStat{}
	for _, c := range cuts {
		s := stats[c.Type]
		s.Count++
		s.Duration += c.End - c.Start
		stats[c.Type] = s
	}
	return stats
}

func reviewNotes(candidates []CutCandidate) (int, []string) {
	seen := map[string]bool{}
	var protected []string
	review := 0
	for _, c := range candidates {
		if c.Decision == "review" {
			review++
		}
		if c.Safety.ProtectedContext && c.Text != "" {
			t := normalizePhrase(c.Text)
			if !seen[t] {
				seen[t] = true
				protected = append(protected, c.Text)
			}
		}
	}
	return review, protected
}

func writeCandidatePreviewMD(path string, plan CutPlan) error {
	var b strings.Builder
	fmt.Fprint(&b, "# AutoClip 剪辑候选预览\n\n")
	fmt.Fprintf(&b, "- 预设：%s\n", plan.Preset)
	fmt.Fprintf(&b, "- 已剪：%d\n", countCandidates(plan.Candidates, "remove"))
	fmt.Fprintf(&b, "- 建议人工看：%d\n\n", countCandidates(plan.Candidates, "review"))
	fmt.Fprintln(&b, "| 状态 | 类型 | 时间 | 时长 | 文本 | 原因 |")
	fmt.Fprintln(&b, "|---|---|---|---:|---|---|")
	for _, c := range plan.Candidates {
		status := "建议人工看"
		if c.Decision == "remove" {
			status = "已剪"
		}
		if c.Safety.ProtectedContext {
			status = "保护词保留"
		}
		fmt.Fprintf(&b, "| %s | %s | %s-%s | %s | %s | %s |\n", status, c.Type, srtTime(c.Start), srtTime(c.End), formatDuration(c.End-c.Start), markdownCell(c.Text), markdownCell(c.Reason))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeCandidatePreviewHTML(path string, plan CutPlan) error {
	var b strings.Builder
	fmt.Fprint(&b, "<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\"><title>AutoClip 剪辑候选预览</title>")
	fmt.Fprint(&b, "<style>body{font-family:-apple-system,BlinkMacSystemFont,'PingFang SC',sans-serif;margin:32px;line-height:1.5;color:#1f2328}table{border-collapse:collapse;width:100%;font-size:14px}th,td{border:1px solid #d0d7de;padding:8px;text-align:left;vertical-align:top}.remove{background:#effaf2}.review{background:#fff8c5}.protected{background:#f6f8fa}.time{white-space:nowrap}</style></head><body>")
	fmt.Fprintf(&b, "<h1>AutoClip 剪辑候选预览</h1><p>预设：%s；已剪：%d；建议人工看：%d。</p>", htmlEscape(plan.Preset), countCandidates(plan.Candidates, "remove"), countCandidates(plan.Candidates, "review"))
	fmt.Fprint(&b, "<table><thead><tr><th>状态</th><th>类型</th><th>时间</th><th>时长</th><th>文本</th><th>原因</th></tr></thead><tbody>")
	for _, c := range plan.Candidates {
		status := "建议人工看"
		class := "review"
		if c.Decision == "remove" {
			status = "已剪"
			class = "remove"
		}
		if c.Safety.ProtectedContext {
			status = "保护词保留"
			class = "protected"
		}
		fmt.Fprintf(&b, "<tr class=\"%s\"><td>%s</td><td>%s</td><td class=\"time\">%s-%s</td><td>%s</td><td>%s</td><td>%s</td></tr>", class, htmlEscape(status), htmlEscape(c.Type), srtTime(c.Start), srtTime(c.End), formatDuration(c.End-c.Start), htmlEscape(c.Text), htmlEscape(c.Reason))
	}
	fmt.Fprint(&b, "</tbody></table></body></html>")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func countCandidates(candidates []CutCandidate, decision string) int {
	n := 0
	for _, c := range candidates {
		if c.Decision == decision {
			n++
		}
	}
	return n
}

func htmlEscape(s string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;")
	return replacer.Replace(s)
}

func buildTranscriptReviewPack(sourcePath string, tr Transcript, context string, glossary []string) TranscriptReviewPack {
	instructions := []string{
		"你是字幕校对 Agent。只修正明显的同音词、专名、术语、数字、英文缩写、断句和标点。",
		"不要改写语气、不要润色成书面语、不要增删说话人的真实意思。",
		"保留每个 segment 的 id、start、end；只改 text。时间戳必须保持不变。",
		"输出 corrected transcript JSON，结构仍为 schema_version/language/segments/metadata。",
	}
	return TranscriptReviewPack{
		SchemaVersion:    schemaVersion,
		Type:             "agent_transcript_review",
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
		SourceTranscript: absPath(sourcePath),
		Language:         valueOr(tr.Language, "auto"),
		Context:          context,
		Glossary:         glossary,
		Instructions:     instructions,
		Issues:           detectTranscriptIssues(tr, glossary),
		Segments:         tr.Segments,
	}
}

func detectTranscriptIssues(tr Transcript, glossary []string) []TranscriptIssue {
	var issues []TranscriptIssue
	confusions := map[string][]string{
		"在":     []string{"再"},
		"再":     []string{"在"},
		"的":     []string{"地", "得"},
		"地":     []string{"的", "得"},
		"得":     []string{"的", "地"},
		"做":     []string{"作"},
		"作":     []string{"做"},
		"像":     []string{"向", "象"},
		"剪辑":    []string{"简辑"},
		"字幕":    []string{"字母"},
		"模型":    []string{"魔形"},
		"校准":    []string{"矫准", "较准"},
		"转写":    []string{"撰写"},
		"灵溪":    []string{"灵犀", "WPS 灵犀"},
		"灵西":    []string{"灵犀", "WPS 灵犀"},
		"零溪":    []string{"灵犀", "WPS 灵犀"},
		"零犀":    []string{"灵犀", "WPS 灵犀"},
		"Agent": []string{"智能体", "代理"},
		"AI":    []string{"爱"},
	}
	for _, seg := range tr.Segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			issues = append(issues, TranscriptIssue{SegmentID: seg.ID, Start: seg.Start, End: seg.End, Text: seg.Text, Reason: "空字幕片段，请确认是否为静音、漏识别或需要删除"})
			continue
		}
		for key, hints := range confusions {
			if strings.Contains(text, key) {
				issues = append(issues, TranscriptIssue{SegmentID: seg.ID, Start: seg.Start, End: seg.End, Text: seg.Text, Reason: "包含常见同音/近音易混词：" + key, Hints: hints})
				break
			}
		}
		for _, term := range glossary {
			if term == "" {
				continue
			}
			if strings.Contains(strings.ToLower(text), strings.ToLower(term)) {
				issues = append(issues, TranscriptIssue{SegmentID: seg.ID, Start: seg.Start, End: seg.End, Text: seg.Text, Reason: "包含用户提供术语：" + term})
				break
			}
		}
	}
	return issues
}

func writeTranscriptReviewMD(path string, pack TranscriptReviewPack) error {
	var b strings.Builder
	fmt.Fprint(&b, "# AutoClip Agent 字幕校字包\n\n")
	fmt.Fprintf(&b, "- 源 transcript：%s\n", pack.SourceTranscript)
	fmt.Fprintf(&b, "- 语言：%s\n", pack.Language)
	if pack.Context != "" {
		fmt.Fprintf(&b, "- 素材上下文：%s\n", pack.Context)
	}
	if len(pack.Glossary) > 0 {
		fmt.Fprintf(&b, "- 术语/专名：%s\n", strings.Join(pack.Glossary, "、"))
	}
	fmt.Fprint(&b, "\n## 校字规则\n\n")
	for _, rule := range pack.Instructions {
		fmt.Fprintf(&b, "- %s\n", rule)
	}
	fmt.Fprint(&b, "\n## 可疑片段\n\n")
	if len(pack.Issues) == 0 {
		fmt.Fprint(&b, "暂无自动标记的可疑片段，仍需通读全文。\n")
	} else {
		fmt.Fprintln(&b, "| ID | 时间 | 原文 | 原因 | 建议 |")
		fmt.Fprintln(&b, "|---:|---|---|---|---|")
		for _, issue := range pack.Issues {
			fmt.Fprintf(&b, "| %d | %s-%s | %s | %s | %s |\n", issue.SegmentID, srtTime(issue.Start), srtTime(issue.End), markdownCell(issue.Text), markdownCell(issue.Reason), markdownCell(strings.Join(issue.Hints, " / ")))
		}
	}
	fmt.Fprint(&b, "\n## 全量字幕\n\n")
	fmt.Fprintln(&b, "| ID | 时间 | 文本 |")
	fmt.Fprintln(&b, "|---:|---|---|")
	for _, seg := range pack.Segments {
		fmt.Fprintf(&b, "| %d | %s-%s | %s |\n", seg.ID, srtTime(seg.Start), srtTime(seg.End), markdownCell(seg.Text))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func applyTranscriptCorrection(original, proposed Transcript, sourcePath string) (Transcript, int, error) {
	if len(original.Segments) == 0 {
		return Transcript{}, 0, errors.New("原 transcript 没有片段")
	}
	if len(proposed.Segments) == 0 {
		return Transcript{}, 0, errors.New("校字结果没有片段")
	}
	byID := map[int]TranscriptSeg{}
	for _, seg := range proposed.Segments {
		byID[seg.ID] = seg
	}
	corrected := original
	corrected.SchemaVersion = schemaVersion
	corrected.Language = valueOr(proposed.Language, original.Language)
	if corrected.Metadata == nil {
		corrected.Metadata = map[string]string{}
	}
	corrected.Metadata["corrected_by"] = "agent"
	corrected.Metadata["correction_source"] = absPath(sourcePath)
	corrected.Metadata["correction_applied_at"] = time.Now().UTC().Format(time.RFC3339)
	changes := 0
	for i, base := range original.Segments {
		next, ok := byID[base.ID]
		if !ok && i < len(proposed.Segments) {
			next = proposed.Segments[i]
			ok = true
		}
		if !ok {
			return Transcript{}, 0, fmt.Errorf("校字结果缺少 segment %d", base.ID)
		}
		text := strings.TrimSpace(next.Text)
		if text == "" {
			text = strings.TrimSpace(base.Text)
		}
		if text != strings.TrimSpace(base.Text) {
			changes++
		}
		corrected.Segments[i].Text = text
	}
	cleaned, removed, merged := cleanupCorrectedTranscriptSegments(corrected.Segments)
	corrected.Segments = cleaned
	if removed > 0 || merged > 0 {
		corrected.Metadata["cleanup_removed_segments"] = strconv.Itoa(removed)
		corrected.Metadata["cleanup_merged_duplicates"] = strconv.Itoa(merged)
	}
	return corrected, changes, nil
}

func cleanupCorrectedTranscriptSegments(segments []TranscriptSeg) ([]TranscriptSeg, int, int) {
	cleaned := make([]TranscriptSeg, 0, len(segments))
	removed := 0
	merged := 0
	for _, seg := range segments {
		seg.Text = strings.TrimSpace(seg.Text)
		if seg.Text == "" || seg.End <= seg.Start+0.01 {
			removed++
			continue
		}
		if len(cleaned) > 0 {
			prev := &cleaned[len(cleaned)-1]
			gap := seg.Start - prev.End
			if normalizedSubtitleText(prev.Text) == normalizedSubtitleText(seg.Text) && gap >= -0.05 && gap <= 0.25 {
				if seg.End > prev.End {
					prev.End = seg.End
				}
				prev.Words = nil
				merged++
				continue
			}
		}
		cleaned = append(cleaned, seg)
	}
	return cleaned, removed, merged
}

func normalizedSubtitleText(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	replacer := strings.NewReplacer(
		" ", "", "\t", "", "\n", "",
		"，", "", "。", "", "？", "", "！", "", "、", "", "：", "", "；", "",
		",", "", ".", "", "?", "", "!", "", ":", "", ";", "",
	)
	return replacer.Replace(s)
}

func markdownCell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.TrimSpace(s)
}

func writeBatchReport(path string, rows []map[string]interface{}) error {
	var b strings.Builder
	fmt.Fprint(&b, "# AutoClip 批处理报告\n\n")
	ok := 0
	skipped := 0
	for _, row := range rows {
		if row["ok"] == true {
			ok++
		}
		if row["skipped"] == true {
			skipped++
		}
	}
	fmt.Fprintf(&b, "- 总数：%d\n- 成功：%d\n- 跳过：%d\n- 失败：%d\n\n", len(rows), ok, skipped, len(rows)-ok)
	fmt.Fprintln(&b, "| 文件 | 状态 | 输出/错误 |")
	fmt.Fprintln(&b, "|---|---|---|")
	for _, row := range rows {
		status := "失败"
		detail := fmt.Sprint(row["error"])
		if row["ok"] == true {
			status = "成功"
			if row["skipped"] == true {
				status = "已跳过"
			}
			detail = fmt.Sprint(row["output"])
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", row["input"], status, strings.ReplaceAll(detail, "|", "\\|"))
	}
	ranking := append([]map[string]interface{}{}, rows...)
	sort.SliceStable(ranking, func(i, j int) bool {
		return floatFromAny(ranking[i]["removed_seconds"]) > floatFromAny(ranking[j]["removed_seconds"])
	})
	fmt.Fprint(&b, "\n## 节省时长排行\n\n")
	fmt.Fprintln(&b, "| 文件 | 移除时长 |")
	fmt.Fprintln(&b, "|---|---:|")
	for _, row := range ranking {
		if row["ok"] != true {
			continue
		}
		fmt.Fprintf(&b, "| %s | %s |\n", row["input"], formatDuration(floatFromAny(row["removed_seconds"])))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func makeOutputPaths(input, out string, render bool, renderMode string) OutputPaths {
	absInput := absPath(input)
	dir := filepath.Dir(absInput)
	stem := strings.TrimSuffix(filepath.Base(absInput), filepath.Ext(absInput))
	if out != "" {
		out = expandHome(out)
		if strings.HasSuffix(out, string(os.PathSeparator)) || isDir(out) || filepath.Ext(out) == "" {
			dir = out
		} else if render {
			dir = filepath.Dir(out)
		} else {
			dir = out
		}
	}
	base := filepath.Join(dir, stem+".autoclip")
	video := base + ".mp4"
	if renderMode == "audio-only" {
		video = base + ".m4a"
	}
	if out != "" && render && !isDir(out) && filepath.Ext(out) != "" {
		video = out
		base = strings.TrimSuffix(out, filepath.Ext(out))
	}
	return OutputPaths{
		Base:           base,
		Video:          video,
		Cuts:           base + ".cuts.json",
		TranscriptSRT:  base + ".transcript.srt",
		TranscriptASS:  base + ".transcript.ass",
		TranscriptJSON: base + ".transcript.json",
		ReviewJSON:     base + ".transcript.review.json",
		ReviewMD:       base + ".transcript.review.md",
		CorrectedJSON:  base + ".transcript.corrected.json",
		CorrectedSRT:   base + ".transcript.corrected.srt",
		CorrectedASS:   base + ".transcript.corrected.ass",
		PreviewMD:      base + ".preview.md",
		PreviewHTML:    base + ".preview.html",
		Report:         base + ".report.md",
		Manifest:       base + ".manifest.json",
		Audio:          base + ".audio.wav",
	}
}

func makeTranscriptSidecarPaths(transcriptPath, out string) OutputPaths {
	base := transcriptSidecarBase(strings.TrimSuffix(absPath(transcriptPath), filepath.Ext(transcriptPath)))
	if out != "" {
		out = expandHome(out)
		if strings.HasSuffix(out, string(os.PathSeparator)) || isDir(out) || filepath.Ext(out) == "" {
			base = filepath.Join(out, filepath.Base(base))
		} else {
			base = transcriptSidecarBase(strings.TrimSuffix(out, filepath.Ext(out)))
		}
	}
	return OutputPaths{
		Base:           base,
		TranscriptJSON: base + ".json",
		TranscriptSRT:  base + ".srt",
		TranscriptASS:  base + ".ass",
		ReviewJSON:     base + ".review.json",
		ReviewMD:       base + ".review.md",
		CorrectedJSON:  base + ".corrected.json",
		CorrectedSRT:   base + ".corrected.srt",
		CorrectedASS:   base + ".corrected.ass",
	}
}

func transcriptSidecarBase(base string) string {
	for _, suffix := range []string{".corrected", ".review"} {
		if strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix)
		}
	}
	return base
}

func batchReportDir(inputDir, out string) string {
	if out != "" {
		return expandHome(out)
	}
	return inputDir
}

func presetByName(name string) (Preset, error) {
	switch name {
	case "", "balanced":
		return Preset{Name: "balanced", MinFillerConfidence: 0.75, MinSilenceSeconds: 0.85, PrePadMS: 50, PostPadMS: 90, MergeGapMS: 180, MaxCutSecondsWithoutConfirmation: 4.0, MinKeptSegmentMS: 220, EdgeGuardMS: 250}, nil
	case "conservative":
		return Preset{Name: "conservative", MinFillerConfidence: 0.85, MinSilenceSeconds: 1.20, PrePadMS: 80, PostPadMS: 120, MergeGapMS: 160, MaxCutSecondsWithoutConfirmation: 2.5, MinKeptSegmentMS: 260, EdgeGuardMS: 350}, nil
	case "aggressive":
		return Preset{Name: "aggressive", MinFillerConfidence: 0.65, MinSilenceSeconds: 0.55, PrePadMS: 20, PostPadMS: 60, MergeGapMS: 220, MaxCutSecondsWithoutConfirmation: 8.0, MinKeptSegmentMS: 160, EdgeGuardMS: 180}, nil
	default:
		return Preset{}, fmt.Errorf("未知剪辑预设 %s，可选：conservative, balanced, aggressive", name)
	}
}

func fillerSet(language, custom string) map[string]bool {
	values := []string{"um", "uh", "er", "erm", "ah", "hmm", "like", "you know", "i mean", "sort of", "kind of", "嗯", "呃", "啊", "额", "那个", "就是", "这个", "怎么说"}
	if custom != "" {
		values = splitList(custom)
	}
	return tokenSet(values)
}

func protectSet(language, custom string) map[string]bool {
	values := []string{"然后", "所以", "但是", "因为", "如果", "对"}
	if custom != "" {
		values = splitList(custom)
	}
	return tokenSet(values)
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func tokenSet(values []string) map[string]bool {
	m := map[string]bool{}
	for _, v := range values {
		m[normalizePhrase(v)] = true
	}
	return m
}

func normalizeToken(s string) string {
	return normalizePhrase(s)
}

func normalizePhrase(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer("，", "", "。", "", "、", "", "？", "", "！", "", ",", "", ".", "", "?", "", "!", "", "“", "", "”", "", "\"", "", "'", "")
	return strings.Join(strings.Fields(replacer.Replace(s)), " ")
}

func containsConfiguredFiller(text string, fillers map[string]bool) bool {
	for f := range fillers {
		if f != "" && strings.Contains(text, f) {
			return true
		}
	}
	return false
}

func confidenceOK(conf, min float64) bool {
	return conf == 0 || conf >= min
}

func listMediaFiles(root string, recursive bool) ([]string, error) {
	supported := map[string]bool{".mp4": true, ".mov": true, ".m4v": true, ".mkv": true, ".webm": true, ".mp3": true, ".wav": true, ".m4a": true, ".aac": true}
	var files []string
	if recursive {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && supported[strings.ToLower(filepath.Ext(path))] {
				files = append(files, path)
			}
			return nil
		})
		return files, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			p := filepath.Join(root, e.Name())
			if supported[strings.ToLower(filepath.Ext(p))] {
				files = append(files, p)
			}
		}
	}
	sort.Strings(files)
	return files, nil
}

func writeSRT(path string, tr Transcript) error {
	var b strings.Builder
	for i, seg := range tr.Segments {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(seg.Start), srtTime(seg.End), strings.TrimSpace(seg.Text))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeASS(path string, tr Transcript, style string) error {
	fontSize := 42
	marginV := 80
	if style == "large" {
		fontSize = 54
		marginV = 110
	}
	var b strings.Builder
	fmt.Fprint(&b, "[Script Info]\nScriptType: v4.00+\nPlayResX: 1080\nPlayResY: 1920\nWrapStyle: 2\nScaledBorderAndShadow: yes\n\n")
	fmt.Fprint(&b, "[V4+ Styles]\n")
	fmt.Fprint(&b, "Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	fmt.Fprintf(&b, "Style: Default,PingFang SC,%d,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,4,1,2,80,80,%d,1\n\n", fontSize, marginV)
	fmt.Fprint(&b, "[Events]\n")
	fmt.Fprint(&b, "Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	for _, seg := range tr.Segments {
		text := assText(seg.Text)
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assTime(seg.Start), assTime(seg.End), text)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func assText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "{", "\\{")
	s = strings.ReplaceAll(s, "}", "\\}")
	s = strings.ReplaceAll(s, "\n", "\\N")
	return s
}

func assTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	cs := int(math.Round(sec * 100))
	h := cs / 360000
	cs %= 360000
	m := cs / 6000
	cs %= 6000
	s := cs / 100
	cs %= 100
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}

func srtTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int(math.Round(sec * 1000))
	h := ms / 3600000
	ms %= 3600000
	m := ms / 60000
	ms %= 60000
	s := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func formatDuration(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	total := int(math.Round(sec))
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func durationKept(segments []TimeSegment) float64 {
	total := 0.0
	for _, s := range segments {
		total += math.Max(0, s.End-s.Start)
	}
	return round3(total)
}

func classifyProcessError(err error, fallback int) int {
	if errors.Is(err, errCloudReserved) {
		return exitCloudConfigRequired
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "ffmpeg"), strings.Contains(msg, "whisper-cli"), strings.Contains(msg, "模型"):
		return exitDependencyFailure
	case strings.Contains(msg, "转写"):
		return exitTranscriptionFailure
	}
	return fallback
}

func extractTarGz(path, dst string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	cleanDst, _ := filepath.Abs(dst)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dst, hdr.Name)
		cleanTarget, _ := filepath.Abs(target)
		if !strings.HasPrefix(cleanTarget, cleanDst+string(os.PathSeparator)) && cleanTarget != cleanDst {
			return errors.New("tar 包含非法路径")
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}

func extractZipBinary(path, member, dst string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	var chosen *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if member != "" && f.Name == member {
			chosen = f
			break
		}
		if member == "" && chosen == nil {
			chosen = f
		}
	}
	if chosen == nil {
		return errors.New("zip 中未找到目标二进制")
	}
	in, err := chosen.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}

func firstDir(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(root, e.Name()), nil
		}
	}
	return "", errors.New("解压后未找到源码目录")
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeJSON(w io.Writer, v interface{}) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func readJSONFile(path string, v interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}

func writeJSONFile(path string, v interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func commaSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out[t] = true
		}
	}
	return out
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && (st.Mode()&os.ModeCharDevice) != 0
}

func dependencySource(path, home string) string {
	if strings.HasPrefix(path, filepath.Join(home, "bin")) {
		return "autoclip-cache"
	}
	return "system-path"
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func shortID(s string) string {
	sum := sha1.Sum([]byte(s + time.Now().String()))
	return hex.EncodeToString(sum[:])[:6]
}

func firstPresent(m map[string]interface{}, keys ...string) interface{} {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

func stringFromAny(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		if x == nil {
			return ""
		}
		return fmt.Sprint(x)
	}
}

func floatFromAny(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	default:
		return 0
	}
}

func secondsFromAny(v interface{}) float64 {
	switch x := v.(type) {
	case string:
		x = strings.TrimSpace(x)
		if strings.Contains(x, ":") {
			parts := strings.Split(x, ":")
			if len(parts) == 3 {
				h, _ := strconv.ParseFloat(parts[0], 64)
				m, _ := strconv.ParseFloat(parts[1], 64)
				s, _ := strconv.ParseFloat(strings.ReplaceAll(parts[2], ",", "."), 64)
				return h*3600 + m*60 + s
			}
		}
		f, _ := strconv.ParseFloat(x, 64)
		return f
	default:
		return floatFromAny(v)
	}
}
