package repl

import (
	"testing"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
)

func TestBuildConsoleDownloadRequest(t *testing.T) {
	request, err := buildConsoleDownloadRequest([]string{
		"https://example.test/base.txz", "--type", "base-rootfs", "--filename", "base.txz", "--ignore-tls", "--raw",
	})
	if err != nil {
		t.Fatalf("build console download request: %v", err)
	}
	request, err = normalizeDownloadRequest(request)
	if err != nil {
		t.Fatalf("normalize download request: %v", err)
	}
	if request.DownloadType != utilitiesModels.DownloadUTypeBase {
		t.Fatalf("download type = %q, want %q", request.DownloadType, utilitiesModels.DownloadUTypeBase)
	}
	if request.Filename == nil || *request.Filename != "base.txz" {
		t.Fatalf("filename = %v, want base.txz", request.Filename)
	}
	if request.IgnoreTLS == nil || !*request.IgnoreTLS {
		t.Fatalf("ignore TLS = %v, want true", request.IgnoreTLS)
	}
	if request.AutomaticExtraction == nil || !*request.AutomaticExtraction {
		t.Fatalf("automatic extraction = %v, want true for base downloads", request.AutomaticExtraction)
	}
	if request.AutomaticRawConversion == nil || !*request.AutomaticRawConversion {
		t.Fatalf("automatic raw conversion = %v, want true", request.AutomaticRawConversion)
	}
}

func TestNormalizeDownloadRequestAcceptsOtherAliases(t *testing.T) {
	for _, value := range []string{"other", "uncategorized", "uncategoried"} {
		t.Run(value, func(t *testing.T) {
			request, err := normalizeDownloadRequest(utilitiesServiceInterfaces.DownloadFileRequest{
				URL:          "https://example.test/file",
				DownloadType: utilitiesModels.DownloadUType(value),
			})
			if err != nil {
				t.Fatalf("normalize download request: %v", err)
			}
			if request.DownloadType != utilitiesModels.DownloadUTypeOther {
				t.Fatalf("download type = %q, want %q", request.DownloadType, utilitiesModels.DownloadUTypeOther)
			}
		})
	}
}

func TestBuildConsoleDownloadRequestRejectsUnknownOption(t *testing.T) {
	_, err := buildConsoleDownloadRequest([]string{"https://example.test/file", "--unknown"})
	if err == nil {
		t.Fatal("expected unknown option error")
	}
}
