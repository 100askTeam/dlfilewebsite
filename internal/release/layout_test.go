package release

import "testing"

func TestPublishedLayoutIsProductScopedUnderTools(t *testing.T) {
	if got := PublishedPath("lynx", "stable", "0.9.0"); got != "Tools/lynx/releases/stable/0.9.0" {
		t.Fatalf("unexpected LYNX path: %q", got)
	}
	if got := PublishedPath("usbtoolbox", "beta", "1.2.0-rc.1"); got != "Tools/usbtoolbox/releases/beta/1.2.0-rc.1" {
		t.Fatalf("unexpected USBToolBox path: %q", got)
	}
}

func TestManagedLayoutAndLegacyRedirect(t *testing.T) {
	for _, candidate := range []string{
		"releases",
		"releases/lynx/stable/0.9.0/file.exe",
		"Tools/lynx/releases",
		`/Tools\\usbtoolbox\\releases\\stable`,
	} {
		if !IsManagedPublishedPath(candidate) {
			t.Fatalf("managed path not recognized: %q", candidate)
		}
	}
	for _, candidate := range []string{"Tools", "Tools/lynx", "Tools/lynx/manual", "downloads/releases.zip"} {
		if IsManagedPublishedPath(candidate) {
			t.Fatalf("manual path was reserved: %q", candidate)
		}
	}
	if got, ok := LegacyRedirectPath("/releases/lynx/stable/0.9.0/file.exe"); !ok || got != "/Tools/lynx/releases/stable/0.9.0/file.exe" {
		t.Fatalf("unexpected legacy redirect: %q, %v", got, ok)
	}
	if _, ok := LegacyRedirectPath("/releases/Invalid Product/file.exe"); ok {
		t.Fatal("invalid product must not redirect")
	}
}
