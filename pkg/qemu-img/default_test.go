package qemuimg

import (
	"errors"
	"testing"
)

type stubQemuImg struct {
	checkToolsErr error
	infoResp      *ImageInfo
	infoErr       error
	convertErr    error

	checkToolsCalled bool
	infoCalled       bool
	convertCalled    bool

	lastInfoPath string
	lastSrc      string
	lastDst      string
	lastFmt      DiskFormat
}

func (s *stubQemuImg) CheckTools() error {
	s.checkToolsCalled = true
	return s.checkToolsErr
}

func (s *stubQemuImg) Info(path string) (*ImageInfo, error) {
	s.infoCalled = true
	s.lastInfoPath = path
	return s.infoResp, s.infoErr
}

func (s *stubQemuImg) InfoBackingChain(_ string) ([]*ImageInfo, error) {
	return nil, nil
}

func (s *stubQemuImg) Convert(src, dst string, outFmt DiskFormat) error {
	s.convertCalled = true
	s.lastSrc = src
	s.lastDst = dst
	s.lastFmt = outFmt
	return s.convertErr
}

func TestDefaultWrappersDelegateToConfiguredImplementation(t *testing.T) {
	orig := qi
	t.Cleanup(func() { qi = orig })

	stub := &stubQemuImg{
		infoResp: &ImageInfo{Format: "qcow2"},
	}
	SetDefault(stub)

	if err := CheckTools(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := Info("/tmp/a.qcow2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil || info.Format != "qcow2" {
		t.Fatalf("unexpected info: %#v", info)
	}

	if err := Convert("/tmp/a.qcow2", "/tmp/a.raw", FormatRaw); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !stub.checkToolsCalled {
		t.Fatal("expected CheckTools to be called")
	}
	if !stub.infoCalled || stub.lastInfoPath != "/tmp/a.qcow2" {
		t.Fatalf("unexpected info call state: called=%v path=%q", stub.infoCalled, stub.lastInfoPath)
	}
	if !stub.convertCalled {
		t.Fatal("expected Convert to be called")
	}
	if stub.lastSrc != "/tmp/a.qcow2" || stub.lastDst != "/tmp/a.raw" || stub.lastFmt != FormatRaw {
		t.Fatalf("unexpected convert args: src=%q dst=%q fmt=%q", stub.lastSrc, stub.lastDst, stub.lastFmt)
	}
}

func TestSetDefaultIgnoresNil(t *testing.T) {
	orig := qi
	t.Cleanup(func() { qi = orig })

	first := &stubQemuImg{}
	SetDefault(first)
	SetDefault(nil)

	if qi != first {
		t.Fatal("expected SetDefault(nil) to keep current default")
	}
}

func TestDefaultWrappersPropagateErrors(t *testing.T) {
	orig := qi
	t.Cleanup(func() { qi = orig })

	checkErr := errors.New("check failed")
	infoErr := errors.New("info failed")
	convertErr := errors.New("convert failed")

	stub := &stubQemuImg{
		checkToolsErr: checkErr,
		infoErr:       infoErr,
		convertErr:    convertErr,
	}
	SetDefault(stub)

	if err := CheckTools(); !errors.Is(err, checkErr) {
		t.Fatalf("unexpected CheckTools error: %v", err)
	}
	if _, err := Info("/tmp/a.qcow2"); !errors.Is(err, infoErr) {
		t.Fatalf("unexpected Info error: %v", err)
	}
	if err := Convert("/tmp/a.qcow2", "/tmp/a.raw", FormatRaw); !errors.Is(err, convertErr) {
		t.Fatalf("unexpected Convert error: %v", err)
	}
}
