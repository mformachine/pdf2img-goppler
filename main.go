package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPort                   = "5000"
	defaultTmpDir                 = "/tmp"
	defaultMaxFileSizeMB    int64 = 50
	defaultMediaTTLMinutes        = 60
	defaultCleanupIntervalMinutes = 60
	maxMemoryParseForm      int64 = 32 << 20
)

var (
	port                   = getEnv("PORT", defaultPort)
	tmpDir                 = getEnv("TMP_DIR", defaultTmpDir)
	mediaDir               = filepath.Join(".", "media")
	maxFileSizeMB          = getEnvInt64("MAX_FILE_SIZE_MB", defaultMaxFileSizeMB)
	mediaTTLMinutes        = getEnvInt("MEDIA_TTL_MINUTES", defaultMediaTTLMinutes)
	cleanupIntervalMinutes = getEnvInt("CLEANUP_INTERVAL_MINUTES", defaultCleanupIntervalMinutes)
)

type errorResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type imagesResponse struct {
	Images []string `json:"images"`
}

func main() {
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		panic(err)
	}

	startMediaCleanupJob()

	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/healthz", healthHandler)
	http.HandleFunc("/pdftocairo", withUpload(pdftocairoHandler))
	http.HandleFunc("/pdftoppm", withUpload(pdftoppmHandler))
	http.HandleFunc("/pdftohtml", withUpload(pdftohtmlHandler))
	http.HandleFunc("/pdfinfo", withUpload(pdfinfoHandler))
	http.HandleFunc("/pdftotext", withUpload(pdftotextHandler))
	http.Handle("/media/", http.StripPrefix("/media/", http.FileServer(http.Dir(mediaDir))))

	fmt.Printf("PDF service (Poppler) running on port %s\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		panic(err)
	}
}

func rootHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Welcome to chanmo/poppler"))
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type uploadHandler func(http.ResponseWriter, *http.Request, string)

func withUpload(next uploadHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFileSizeMB*1024*1024)

		if err := r.ParseMultipartForm(maxMemoryParseForm); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{
					Success: false,
					Message: fmt.Sprintf("file is too large. max size is %dMB.", maxFileSizeMB),
				})
				return
			}

			writeJSON(w, http.StatusBadRequest, errorResponse{
				Success: false,
				Message: "invalid multipart form data",
			})
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusOK, errorResponse{
				Success: false,
				Message: "file is required.",
			})
			return
		}
		defer file.Close()

		uploadedPath, err := saveUploadedFile(file, header)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{
				Success: false,
				Message: "failed to save uploaded file",
			})
			return
		}
		defer removeFileQuietly(uploadedPath)

		next(w, r, uploadedPath)
	}
}

func pdftocairoHandler(w http.ResponseWriter, r *http.Request, uploadedPath string) {
	runImageCommand(w, r, uploadedPath, "pdftocairo")
}

func pdftoppmHandler(w http.ResponseWriter, r *http.Request, uploadedPath string) {
	runImageCommand(w, r, uploadedPath, "pdftoppm")
}

func runImageCommand(w http.ResponseWriter, r *http.Request, uploadedPath, command string) {
	format, err := normalizeFormat(r.FormValue("format"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Success: false, Message: err.Error()})
		return
	}

	dpi, err := parseDPI(r.FormValue("dpi"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Success: false, Message: err.Error()})
		return
	}

	outID, err := randomID()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Success: false, Message: "failed to create output id"})
		return
	}

	outDir := filepath.Join(mediaDir, outID)
	if err := os.Mkdir(outDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Success: false, Message: "failed to create output directory"})
		return
	}

	success := false
	defer func() {
		if !success {
			removeDirQuietly(outDir)
		}
	}()

	args := make([]string, 0, 4)
	if dpi > 0 {
		args = append(args, "-r", strconv.Itoa(dpi))
	}

	switch command {
	case "pdftocairo":
		if format == "jpeg" {
			args = append(args, "-jpeg")
		} else {
			args = append(args, "-png")
		}
		args = append(args, uploadedPath, filepath.Join(outDir, "output"))
	case "pdftoppm":
		if format == "jpeg" {
			args = append(args, "-jpeg")
		} else {
			args = append(args, "-png")
		}
		args = append(args, uploadedPath, "output")
	default:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Success: false, Message: "invalid image command"})
		return
	}

	cmd := exec.Command(command, args...)
	if command == "pdftoppm" {
		cmd.Dir = outDir
	}

	output, err := combinedOutput(cmd)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{
			Success: false,
			Message: commandError(command, output, err),
		})
		return
	}

	expectedExt := getExtension(format)
	entries, err := os.ReadDir(outDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Success: false, Message: "failed to read output directory"})
		return
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(strings.ToLower(name), expectedExt) {
			files = append(files, name)
		}
	}

	sort.Strings(files)
	success = true

	resp := imagesResponse{
		Images: make([]string, 0, len(files)),
	}
	for _, name := range files {
		resp.Images = append(resp.Images, "/media/"+outID+"/"+name)
	}

	writeJSON(w, http.StatusOK, resp)
}

func pdftohtmlHandler(w http.ResponseWriter, _ *http.Request, uploadedPath string) {
	args := []string{"-s", "-dataurls", "-noframes", "-stdout", uploadedPath}
	cmd := exec.Command("pdftohtml", args...)

	stdout, stderr, err := runCommand(cmd)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{
			Success: false,
			Message: commandError("pdftohtml", stderr, err),
		})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(stdout)
}

func pdfinfoHandler(w http.ResponseWriter, _ *http.Request, uploadedPath string) {
	cmd := exec.Command("pdfinfo", uploadedPath)

	stdout, stderr, err := runCommand(cmd)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{
			Success: false,
			Message: commandError("pdfinfo", stderr, err),
		})
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(stdout)
}

func pdftotextHandler(w http.ResponseWriter, _ *http.Request, uploadedPath string) {
	cmd := exec.Command("pdftotext", uploadedPath, "-")

	stdout, stderr, err := runCommand(cmd)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{
			Success: false,
			Message: commandError("pdftotext", stderr, err),
		})
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(stdout)
}

func saveUploadedFile(file multipart.File, header *multipart.FileHeader) (string, error) {
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return "", err
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	name, err := randomID()
	if err != nil {
		return "", err
	}

	dstPath := filepath.Join(tmpDir, name+ext)
	dst, err := os.Create(dstPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		return "", err
	}

	return dstPath, nil
}

func normalizeFormat(value string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(value))
	if format == "" {
		return "png", nil
	}
	if format == "jpg" {
		return "jpeg", nil
	}
	if format == "png" || format == "jpeg" {
		return format, nil
	}
	return "", errors.New("format must be one of: png, jpeg, jpg")
}

func parseDPI(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}

	dpi, err := strconv.Atoi(value)
	if err != nil || dpi <= 0 {
		return 0, errors.New("dpi must be a positive integer")
	}

	return dpi, nil
}

func getExtension(format string) string {
	if format == "jpeg" {
		return ".jpg"
	}
	return ".png"
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func removeFileQuietly(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

func removeDirQuietly(path string) {
	if path == "" {
		return
	}
	_ = os.RemoveAll(path)
}

func runCommand(cmd *exec.Cmd) ([]byte, []byte, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func combinedOutput(cmd *exec.Cmd) ([]byte, error) {
	return cmd.CombinedOutput()
}

func commandError(command string, stderr []byte, err error) string {
	message := strings.TrimSpace(string(stderr))
	if message != "" {
		return message
	}
	if err != nil {
		return err.Error()
	}
	return command + " failed"
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func startMediaCleanupJob() {
	_ = cleanupExpiredMedia()

	ticker := time.NewTicker(time.Duration(cleanupIntervalMinutes) * time.Minute)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			_ = cleanupExpiredMedia()
		}
	}()
}

func cleanupExpiredMedia() error {
	entries, err := os.ReadDir(mediaDir)
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-time.Duration(mediaTTLMinutes) * time.Minute)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirPath := filepath.Join(mediaDir, entry.Name())
		info, err := os.Stat(dirPath)
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			removeDirQuietly(dirPath)
		}
	}

	return nil
}

func getEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getEnvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func getEnvInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
