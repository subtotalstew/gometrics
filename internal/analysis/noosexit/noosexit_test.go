package noosexit_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/subtotalstew/gometrics.git/internal/analysis/noosexit"
)

func TestAnalyzer(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, noosexit.Analyzer, "mainpkg", "helperpkg")
}
