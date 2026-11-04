//go:build !windows

/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package converter

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Run the test executable as the external builder, including a real FIFO open
// and process lifetime. No nydus-image installation is needed for these tests.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "unpack" {
		os.Exit(unpackTestBuilder())
	}
	os.Exit(m.Run())
}

func unpackTestBuilder() int {
	directory := os.Getenv("NYDUS_TEST_UNPACK_DIRECTORY")
	mode := os.Getenv("NYDUS_TEST_UNPACK_MODE")
	output := ""
	for i, arg := range os.Args {
		if arg == "--output" && i+1 < len(os.Args) {
			output = os.Args[i+1]
		}
	}
	if directory == "" || output == "" {
		return 91
	}
	marker := filepath.Join(directory, "started")
	if err := os.WriteFile(marker+".tmp", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		return 92
	}
	if err := os.Rename(marker+".tmp", marker); err != nil {
		return 93
	}
	if mode == "cancel" {
		for {
			if _, err := os.Stat(filepath.Join(directory, "release")); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	file, err := os.OpenFile(output, os.O_WRONLY, 0)
	if err != nil {
		return 94
	}
	if mode == "write-error" {
		buffer := make([]byte, 64<<10)
		for {
			if _, err := file.Write(buffer); err != nil {
				return 95
			}
		}
	}
	if _, err := io.WriteString(file, "original layer contents"); err != nil {
		return 96
	}
	if err := file.Close(); err != nil {
		return 97
	}
	if mode == "exit-error" {
		return 23
	}
	return 0
}

type unpackTestReader struct{ *bytes.Reader }

func (unpackTestReader) Close() error { return nil }

func unpackTestBlob(t *testing.T) unpackTestReader {
	t.Helper()
	data := []byte("test bootstrap")
	var header bytes.Buffer
	if err := tar.NewWriter(&header).WriteHeader(&tar.Header{Name: EntryBootstrap, Size: int64(len(data)), Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	// Nydus stores each entry's data before its 512-byte tar header.
	return unpackTestReader{bytes.NewReader(append(data, header.Bytes()[:512]...))}
}

func startUnpackTest(t *testing.T, ctx context.Context, mode string, destination io.Writer) (string, <-chan error) {
	t.Helper()
	directory := t.TempDir()
	t.Setenv("NYDUS_TEST_UNPACK_DIRECTORY", directory)
	t.Setenv("NYDUS_TEST_UNPACK_MODE", mode)
	builder, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	blob := unpackTestBlob(t)
	go func() {
		result <- Unpack(ctx, blob, destination, UnpackOption{BuilderPath: builder, WorkDir: directory})
	}()
	return directory, result
}

func unpackTestPID(t *testing.T, directory string, result <-chan error) int {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if data, err := os.ReadFile(filepath.Join(directory, "started")); err == nil {
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
		select {
		case <-deadline:
			select {
			case err := <-result:
				t.Fatalf("conversion ended before builder started: %v", err)
			default:
				t.Fatal("external builder did not start")
			}
		case <-time.After(time.Millisecond):
		}
	}
}

func finishUnpackTest(t *testing.T, pid int, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		if killErr := syscall.Kill(pid, 0); !errors.Is(killErr, syscall.ESRCH) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Errorf("conversion returned without reaping builder %d: %v", pid, killErr)
		}
		return err
	case <-time.After(2 * time.Second):
		// Clean the intentionally blocked subprocess even when testing old code.
		_ = syscall.Kill(pid, syscall.SIGKILL)
		select {
		case <-result:
		case <-time.After(2 * time.Second):
			t.Error("conversion did not settle after test cleanup")
		}
		t.Error("conversion hung after cancellation or destination failure")
		return errors.New("conversion did not finish")
	}
}

func TestUnpackCancellationReapsBuilder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	directory, result := startUnpackTest(t, ctx, "cancel", io.Discard)
	pid := unpackTestPID(t, directory, result)
	cancel()
	if err := os.WriteFile(filepath.Join(directory, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := finishUnpackTest(t, pid, result); !errors.Is(err, context.Canceled) {
		t.Errorf("expected caller cancellation, got %v", err)
	}
	entries, err := filepath.Glob(filepath.Join(directory, "nydus-converter-*"))
	if err != nil || len(entries) != 0 {
		t.Errorf("conversion work directory retained: %v, %v", entries, err)
	}
}

type unpackErrorWriter struct{ err error }

func (writer unpackErrorWriter) Write([]byte) (int, error) { return 0, writer.err }

func TestUnpackDestinationFailureReapsBuilder(t *testing.T) {
	failure := errors.New("destination is closed")
	directory, result := startUnpackTest(t, context.Background(), "write-error", unpackErrorWriter{failure})
	pid := unpackTestPID(t, directory, result)
	if err := finishUnpackTest(t, pid, result); !errors.Is(err, failure) {
		t.Errorf("expected original destination error, got %v", err)
	}
}

func TestUnpackChecksBuilderExit(t *testing.T) {
	directory, result := startUnpackTest(t, context.Background(), "exit-error", io.Discard)
	pid := unpackTestPID(t, directory, result)
	if err := finishUnpackTest(t, pid, result); err == nil {
		t.Error("successful FIFO read hid the builder exit failure")
	}
}

func TestUnpackPreservesStream(t *testing.T) {
	var output bytes.Buffer
	directory, result := startUnpackTest(t, context.Background(), "success", &output)
	pid := unpackTestPID(t, directory, result)
	if err := finishUnpackTest(t, pid, result); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "original layer contents" {
		t.Fatalf("incorrect layer output: %q", got)
	}
}
