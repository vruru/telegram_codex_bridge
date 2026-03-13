package transcribe

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultWhisperModel = "base"
const installMaxDuration = 20 * time.Minute

const (
	whisperSourceAppManaged = "app-managed"
	whisperSourceHomebrew   = "homebrew"
	whisperSourceExternal   = "external"
)

type WhisperStatus struct {
	Available   bool   `json:"available"`
	Installed   bool   `json:"installed"`
	Source      string `json:"source,omitempty"`
	WhisperPath string `json:"whisper_path,omitempty"`
	PythonPath  string `json:"python_path,omitempty"`
	FFmpegPath  string `json:"ffmpeg_path,omitempty"`
	Version     string `json:"version,omitempty"`
	Model       string `json:"model,omitempty"`
	InstallRoot string `json:"install_root,omitempty"`
	Error       string `json:"error,omitempty"`
}

type WhisperResult struct {
	Text           string
	TranscriptPath string
	Model          string
}

type InstallResult struct {
	Status WhisperStatus `json:"status"`
	Output string        `json:"output,omitempty"`
}

type whisperBackend struct {
	Source      string
	WhisperPath string
	PythonPath  string
	Version     string
	InstallRoot string
}

func Status(ctx context.Context, projectRoot string) WhisperStatus {
	root := resolveProjectRoot(projectRoot)
	status := WhisperStatus{
		Model:       defaultWhisperModel,
		InstallRoot: whisperInstallRoot(root),
	}

	ffmpegPath, ffmpegErr := resolveExecutable("ffmpeg", ffmpegCandidates())
	if ffmpegErr == nil {
		status.FFmpegPath = ffmpegPath
	}

	backend, backendErr := resolveWhisperBackend(ctx, root, status.FFmpegPath)
	if backendErr != nil {
		status.Error = backendErr.Error()
		if ffmpegErr != nil {
			status.Error = strings.TrimSpace(joinOutput(status.Error, fmt.Sprintf("ffmpeg not found: %v", ffmpegErr)))
		}
		return status
	}

	status.Installed = true
	status.Source = backend.Source
	status.WhisperPath = backend.WhisperPath
	status.PythonPath = backend.PythonPath
	status.Version = backend.Version
	if backend.InstallRoot != "" {
		status.InstallRoot = backend.InstallRoot
	}

	if ffmpegErr != nil {
		status.Error = fmt.Sprintf("OpenAI Whisper is installed, but ffmpeg is missing: %v", ffmpegErr)
		return status
	}

	status.Available = true
	return status
}

func Install(ctx context.Context, projectRoot string) (InstallResult, error) {
	root := resolveProjectRoot(projectRoot)
	current := Status(ctx, root)
	if current.Installed && current.Available {
		return InstallResult{
			Status: current,
			Output: installSuccessMessage(current),
		}, nil
	}

	pythonPath, err := resolveExecutable("python3", pythonCandidates())
	if err != nil {
		result := InstallResult{Status: current}
		result.Output = fmt.Sprintf("python3 not found: %v", err)
		return result, fmt.Errorf("%s", result.Output)
	}

	ffmpegPath, err := resolveExecutable("ffmpeg", ffmpegCandidates())
	if err != nil {
		result := InstallResult{Status: current}
		result.Output = fmt.Sprintf("ffmpeg not found: %v\nInstall ffmpeg first, for example: brew install ffmpeg", err)
		return result, fmt.Errorf("%s", result.Output)
	}

	installRoot := whisperInstallRoot(root)
	if err := os.MkdirAll(filepath.Dir(installRoot), 0o755); err != nil {
		return InstallResult{Status: current}, fmt.Errorf("create whisper install directory: %w", err)
	}

	installCtx, cancel := context.WithTimeout(ctx, installMaxDuration)
	defer cancel()

	outputs := make([]string, 0, 4)
	output, err := runCombined(installCtx, pythonPath, installEnv(pythonPath, ffmpegPath), "-m", "venv", installRoot)
	if err != nil {
		return InstallResult{Status: Status(context.Background(), root), Output: output}, fmt.Errorf("create Whisper virtual environment: %s", outputOrError(output, err))
	}
	outputs = append(outputs, output)

	venvPython := privatePythonPath(root)
	if !isExecutableFile(venvPython) {
		result := InstallResult{
			Status: Status(context.Background(), root),
			Output: strings.TrimSpace(joinOutput(outputs...)),
		}
		return result, fmt.Errorf("Whisper virtual environment was created, but %s is missing", venvPython)
	}

	output, err = runCombined(installCtx, venvPython, installEnv(venvPython, ffmpegPath), "-m", "pip", "install", "--upgrade", "pip", "setuptools", "wheel")
	if err != nil {
		failureOutput := append(append([]string{}, outputs...), output)
		return InstallResult{Status: Status(context.Background(), root), Output: strings.TrimSpace(joinOutput(failureOutput...))}, fmt.Errorf("upgrade pip tooling: %s", outputOrError(output, err))
	}
	outputs = append(outputs, output)

	output, err = runCombined(installCtx, venvPython, installEnv(venvPython, ffmpegPath), "-m", "pip", "install", "--upgrade", "openai-whisper")
	if err != nil {
		failureOutput := append(append([]string{}, outputs...), output)
		return InstallResult{Status: Status(context.Background(), root), Output: strings.TrimSpace(joinOutput(failureOutput...))}, fmt.Errorf("install openai-whisper: %s", outputOrError(output, err))
	}
	outputs = append(outputs, output)

	status := Status(context.Background(), root)
	successOutput := append(append([]string{}, outputs...), installSuccessMessage(status))
	result := InstallResult{
		Status: status,
		Output: strings.TrimSpace(joinOutput(successOutput...)),
	}
	if !status.Installed {
		message := status.Error
		if message == "" {
			message = "OpenAI Whisper is still not installed"
		}
		return result, fmt.Errorf("%s", message)
	}
	if !status.Available {
		message := status.Error
		if message == "" {
			message = "OpenAI Whisper is installed, but not ready to use"
		}
		return result, fmt.Errorf("%s", message)
	}
	return result, nil
}

func Transcribe(ctx context.Context, projectRoot, audioPath, outputDir, languageHint string) (WhisperResult, error) {
	status := Status(ctx, projectRoot)
	if !status.Installed {
		return WhisperResult{}, fmt.Errorf("%s", status.Error)
	}
	if !status.Available {
		return WhisperResult{}, fmt.Errorf("%s", status.Error)
	}

	audioPath = strings.TrimSpace(audioPath)
	if audioPath == "" {
		return WhisperResult{}, fmt.Errorf("audio path is required")
	}
	if strings.TrimSpace(outputDir) == "" {
		outputDir = filepath.Dir(audioPath)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return WhisperResult{}, fmt.Errorf("create transcript directory: %w", err)
	}

	args := []string{
		audioPath,
		"--model", defaultWhisperModel,
		"--output_dir", outputDir,
		"--output_format", "txt",
		"--task", "transcribe",
		"--fp16", "False",
		"--verbose", "False",
	}
	if normalizedLanguage := normalizeLanguageHint(languageHint); normalizedLanguage != "" {
		args = append(args, "--language", normalizedLanguage)
	}

	cmd := exec.CommandContext(ctx, status.WhisperPath, args...)
	cmd.Env = append(os.Environ(), executableEnv(status.WhisperPath, status.FFmpegPath)...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return WhisperResult{}, fmt.Errorf("run local Whisper: %s", strings.TrimSpace(joinOutput(stdout.String(), stderr.String(), err.Error())))
	}

	transcriptPath := filepath.Join(outputDir, strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))+".txt")
	body, err := os.ReadFile(transcriptPath)
	if err != nil {
		return WhisperResult{}, fmt.Errorf("read transcript output: %w", err)
	}

	return WhisperResult{
		Text:           strings.TrimSpace(string(body)),
		TranscriptPath: transcriptPath,
		Model:          defaultWhisperModel,
	}, nil
}

func normalizeLanguageHint(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	raw = strings.Split(raw, "-")[0]
	switch raw {
	case "zh", "en", "ja", "ko", "fr", "de", "es", "ru", "it", "pt":
		return raw
	default:
		return ""
	}
}

func resolveWhisperBackend(ctx context.Context, projectRoot, ffmpegPath string) (whisperBackend, error) {
	root := resolveProjectRoot(projectRoot)
	if backend, ok := detectPrivateVenv(ctx, root, ffmpegPath); ok {
		return backend, nil
	}
	if backend, ok := detectExternalWhisper(ctx, ffmpegPath); ok {
		return backend, nil
	}
	return whisperBackend{}, fmt.Errorf("OpenAI Whisper is not installed")
}

func detectPrivateVenv(ctx context.Context, projectRoot, ffmpegPath string) (whisperBackend, bool) {
	root := whisperInstallRoot(projectRoot)
	pythonPath := privatePythonPath(projectRoot)
	whisperPath := privateWhisperPath(projectRoot)
	if !isExecutableFile(pythonPath) || !isExecutableFile(whisperPath) {
		return whisperBackend{}, false
	}

	version, err := runTrimmed(ctx, pythonPath, executableEnv(pythonPath, ffmpegPath), "-c", "import whisper; print(getattr(whisper, '__version__', 'installed'))")
	if err != nil {
		return whisperBackend{}, false
	}

	return whisperBackend{
		Source:      whisperSourceAppManaged,
		WhisperPath: whisperPath,
		PythonPath:  pythonPath,
		Version:     version,
		InstallRoot: root,
	}, true
}

func detectExternalWhisper(ctx context.Context, ffmpegPath string) (whisperBackend, bool) {
	whisperPath, err := resolveExecutable("whisper", whisperCandidates())
	if err != nil {
		return whisperBackend{}, false
	}

	version, err := runTrimmed(ctx, whisperPath, executableEnv(whisperPath, ffmpegPath), "--version")
	if err != nil {
		if _, helpErr := runTrimmed(ctx, whisperPath, executableEnv(whisperPath, ffmpegPath), "--help"); helpErr != nil {
			return whisperBackend{}, false
		}
		version = ""
	}

	return whisperBackend{
		Source:      whisperSourceForPath(whisperPath),
		WhisperPath: whisperPath,
		Version:     version,
	}, true
}

func resolveProjectRoot(projectRoot string) string {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		if exe, err := os.Executable(); err == nil {
			root = filepath.Dir(filepath.Dir(exe))
		}
	}
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
}

func whisperInstallRoot(projectRoot string) string {
	root := resolveProjectRoot(projectRoot)
	return filepath.Join(root, "data", "whisper-venv")
}

func privatePythonPath(projectRoot string) string {
	return filepath.Join(whisperInstallRoot(projectRoot), "bin", "python")
}

func privateWhisperPath(projectRoot string) string {
	return filepath.Join(whisperInstallRoot(projectRoot), "bin", "whisper")
}

func whisperSourceForPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/opt/homebrew/"), strings.HasPrefix(path, "/usr/local/"):
		return whisperSourceHomebrew
	default:
		return whisperSourceExternal
	}
}

func whisperCandidates() []string {
	return []string{
		"/opt/homebrew/bin/whisper",
		"/usr/local/bin/whisper",
	}
}

func resolveExecutable(name string, candidates []string) (string, error) {
	if resolved, err := exec.LookPath(name); err == nil {
		return resolved, nil
	}
	for _, candidate := range candidates {
		if isExecutableFile(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("exec: %q: executable file not found in $PATH", name)
}

func pythonCandidates() []string {
	return []string{
		"/opt/homebrew/bin/python3",
		"/usr/local/bin/python3",
		"/usr/bin/python3",
	}
}

func ffmpegCandidates() []string {
	return []string{
		"/opt/homebrew/bin/ffmpeg",
		"/usr/local/bin/ffmpeg",
		"/usr/bin/ffmpeg",
	}
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

func runTrimmed(ctx context.Context, binary string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := strings.TrimSpace(stdout.String())
	if err != nil {
		if trimmedErr := strings.TrimSpace(stderr.String()); trimmedErr != "" {
			return output, fmt.Errorf("%s", trimmedErr)
		}
		return output, err
	}
	return output, nil
}

func runCombined(ctx context.Context, binary string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(joinOutput(stdout.String(), stderr.String())), err
}

func executableEnv(paths ...string) []string {
	segments := []string{}
	seen := make(map[string]struct{})
	for _, path := range paths {
		dir := strings.TrimSpace(filepath.Dir(path))
		if dir == "" || dir == "." {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		segments = append(segments, dir)
		seen[dir] = struct{}{}
	}
	currentPATH := os.Getenv("PATH")
	if currentPATH != "" {
		segments = append(segments, currentPATH)
	}
	return []string{"PATH=" + strings.Join(segments, ":")}
}

func installEnv(paths ...string) []string {
	env := executableEnv(paths...)
	env = append(env, "PIP_DISABLE_PIP_VERSION_CHECK=1")
	env = append(env, "PYTHONUTF8=1")
	return env
}

func withPreferredPATH() string {
	segments := []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}
	return "PATH=" + strings.Join(segments, ":")
}

func joinOutput(parts ...string) string {
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		lines = append(lines, trimmed)
	}
	return strings.Join(lines, "\n")
}

func installSuccessMessage(status WhisperStatus) string {
	sourceLabel := status.Source
	if sourceLabel == "" {
		sourceLabel = "installed"
	}
	message := fmt.Sprintf("OpenAI Whisper is ready (%s).", sourceLabel)
	if status.Version != "" {
		message += " Version: " + status.Version + "."
	}
	if status.Source == whisperSourceAppManaged && status.InstallRoot != "" {
		message += " Managed at " + status.InstallRoot + "."
	}
	message += " The first transcription may still download the selected model."
	return message
}

func outputOrError(output string, err error) string {
	if strings.TrimSpace(output) != "" {
		return strings.TrimSpace(output)
	}
	if err != nil {
		return err.Error()
	}
	return ""
}
