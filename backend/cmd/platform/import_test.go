//go:build platform

package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestImportMaintenanceIsExclusiveAndQuotasRequired(t *testing.T) {
	for _, test := range []struct {
		args   []string
		reason string
	}{
		{[]string{"--import-local-from=source", "--backup-to=backup"}, "不能组合"},
		{[]string{"--import-local-from=source", "--bootstrap-admin"}, "不能组合"},
		{[]string{"--import-local-from=source"}, "initial-points"},
		{[]string{"--import-local-from=source", "--initial-points=0"}, "storage-limit-bytes"},
		{[]string{"--import-local-from=source", "--password=secret"}, "flag provided but not defined"},
	} {
		if err := run(context.Background(), test.args); err == nil || !strings.Contains(err.Error(), test.reason) {
			t.Fatalf("%v: %v", test.args, err)
		}
	}
}

func TestImportPasswordDoesNotAcceptPipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	original := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = original }()
	if _, err := readPassword(); err == nil || !strings.Contains(err.Error(), "隐藏输入") {
		t.Fatal("管道密码未被拒绝", err)
	}
}
