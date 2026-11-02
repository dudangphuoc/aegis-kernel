package framework_test

import (
	"os/exec"
	"strings"
	"testing"
)

// Framework, db và các binary nền tảng không được phụ thuộc gói nghiệp vụ (modules/):
// nghiệp vụ chỉ đến với runtime dưới dạng gói nạp động.
func TestFrameworkDoesNotImportModules(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "../framework/...", "../db/...", "../cmd/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if strings.HasPrefix(pkg, "github.com/dudangphuoc/aegis-kernel/modules/") {
			t.Errorf("forbidden dependency on business package %s", pkg)
		}
	}
}
