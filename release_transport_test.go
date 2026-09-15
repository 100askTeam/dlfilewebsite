package main

import (
	"os"
	"strings"
	"testing"
)

func TestRecoveryUploaderUsesSingleSourceSCP(t *testing.T) {
	data, err := os.ReadFile("scripts/restore-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	if !strings.Contains(script, `for upload_file in "${upload_files[@]}"`) {
		t.Fatal("recovery uploader must iterate over individual files")
	}
	if !strings.Contains(script, `"$upload_file" "$remote:$remote_incoming/.part-$job_id/"`) {
		t.Fatal("recovery SCP must have exactly one local source per invocation")
	}
	if strings.Contains(script, `"${upload_files[@]}" "$remote:$remote_incoming/.part-$job_id/"`) {
		t.Fatal("multi-source SCP adds a remote -d flag that the restricted command must reject")
	}
}

func TestPublisherUsesSingleSourceSCP(t *testing.T) {
	data, err := os.ReadFile("scripts/publish-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	if !strings.Contains(script, `for upload_file in "${upload_files[@]}"`) {
		t.Fatal("publisher must iterate over individual files")
	}
	if !strings.Contains(script, `"$upload_file" "$remote:$remote_incoming/.part-$job_id/"`) {
		t.Fatal("publisher SCP must have exactly one local source per invocation")
	}
	if strings.Contains(script, `"${upload_files[@]}" "$remote:$remote_incoming/.part-$job_id/"`) {
		t.Fatal("publisher must not trigger the restricted multi-source SCP -d mode")
	}
}
