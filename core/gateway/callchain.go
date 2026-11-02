package gateway

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
)

// nextCallChain đọc X-Aegis-Call-Chain, chặn gọi vòng (module đích đã có trong chuỗi) hoặc chuỗi quá sâu,
// và trả chuỗi mới (đã thêm module đích) để chuyển xuống module.
func nextCallChain(req *http.Request, target string) (string, error) {
	var chain []string
	for _, p := range strings.Split(req.Header.Get(contract.HeaderCallChain), ",") {
		if p = strings.TrimSpace(p); p != "" {
			chain = append(chain, p)
		}
	}
	if slices.Contains(chain, target) {
		return "", fmt.Errorf("call cycle detected: module '%s' already in chain [%s]", target, strings.Join(chain, " -> "))
	}
	if len(chain) >= contract.MaxCallChainHops {
		return "", fmt.Errorf("call chain exceeds %d hops", contract.MaxCallChainHops)
	}
	return strings.Join(append(chain, target), ","), nil
}
