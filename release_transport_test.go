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
	if !strings.Contains(script, `select((.storage // "site") == "site")`) {
		t.Fatal("publisher must upload only assets assigned to download-site storage")
	}
	if !strings.Contains(script, `release-set.json.sig`) {
		t.Fatal("schema v2 publisher must upload the signed manifest envelope")
	}
}

func TestProtocolV4IsRequiredForTieredReleasePublishing(t *testing.T) {
	actionData, err := os.ReadFile(".github/actions/publish-release/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	wrapperData, err := os.ReadFile("deploy/ssh/dl-release-command")
	if err != nil {
		t.Fatal(err)
	}
	action := string(actionData)
	wrapper := string(wrapperData)
	if !strings.Contains(action, `release-channel-probe-v4`) ||
		!strings.Contains(action, `dl-release-command protocol=4`) {
		t.Fatal("shared publishing action must fail closed unless protocol v4 is installed")
	}
	if !strings.Contains(wrapper, `release-channel-probe-v4`) ||
		!strings.Contains(wrapper, `dl-release-command protocol=4`) {
		t.Fatal("restricted server command must expose protocol v4")
	}
}
