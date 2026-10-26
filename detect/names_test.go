// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"fmt"
	"slices"
	"sync"
	"testing"
)

// The name cache answers the same as splitting each time, past its cap
// and from many goroutines at once.
func TestNameCache(t *testing.T) {
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range maxNames + 500 {
				name := fmt.Sprintf("Plugin_%d_Billing_Phone", (i*7+g)%(maxNames+500))
				ni := infoOf([]byte(name))
				want := fmt.Sprintf("plugin_%d_billing_phone", (i*7+g)%(maxNames+500))
				if ni.lower != want || !slices.Equal(ni.tokens, nameTokens(want)) {
					t.Errorf("%s: %+v", name, ni)
					return
				}
				if !ni.test(testPhone, func(_ string, tokens []string) bool { return slices.Contains(tokens, "phone") }) {
					t.Errorf("%s: phone test", name)
					return
				}
			}
		})
	}
	wg.Wait()
	if ni := infoOfString("meta_value"); ni.lower != "meta_value" || len(ni.tokens) != 2 {
		t.Errorf("meta_value: %+v", ni)
	}
	// A memoised answer is the first one given, whatever fn says later.
	ni := infoOfString("Fresh_Name_For_Memo")
	if !ni.test(testCard, func(string, []string) bool { return true }) || !ni.test(testCard, func(string, []string) bool { return false }) {
		t.Error("memo not kept")
	}
	if ni.test(testIP, func(string, []string) bool { return false }) {
		t.Error("tests share a memo bit")
	}
}
