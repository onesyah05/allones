package secure

import (
	"fmt"
	"math"
	"strings"
)

func mathLog(x float64) float64 { return math.Log(x) }

// CVSS31 computes a CVSS v3.1 base score from a vector such as
// CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H
func CVSS31(vector string) (float64, string, error) {
	metrics := map[string]string{}
	for _, part := range strings.Split(strings.TrimSpace(vector), "/") {
		k, v, ok := strings.Cut(part, ":")
		if !ok || k == "CVSS" {
			continue
		}
		metrics[strings.ToUpper(k)] = strings.ToUpper(v)
	}
	need := []string{"AV", "AC", "PR", "UI", "S", "C", "I", "A"}
	for _, k := range need {
		if metrics[k] == "" {
			return 0, "", fmt.Errorf("vector missing %s", k)
		}
	}
	av := map[string]float64{"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2}[metrics["AV"]]
	ac := map[string]float64{"L": 0.77, "H": 0.44}[metrics["AC"]]
	ui := map[string]float64{"N": 0.85, "R": 0.62}[metrics["UI"]]
	changed := metrics["S"] == "C"
	var pr float64
	switch metrics["PR"] {
	case "N":
		pr = 0.85
	case "L":
		pr = map[bool]float64{false: 0.62, true: 0.68}[changed]
	case "H":
		pr = map[bool]float64{false: 0.27, true: 0.5}[changed]
	}
	cia := map[string]float64{"H": 0.56, "L": 0.22, "N": 0}
	c, i, a := cia[metrics["C"]], cia[metrics["I"]], cia[metrics["A"]]
	if av == 0 || ac == 0 || ui == 0 || pr == 0 {
		return 0, "", fmt.Errorf("invalid metric value in %q", vector)
	}
	iss := 1 - (1-c)*(1-i)*(1-a)
	var impact float64
	if changed {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	} else {
		impact = 6.42 * iss
	}
	expl := 8.22 * av * ac * pr * ui
	var score float64
	if impact > 0 {
		if changed {
			score = roundUp(math.Min(1.08*(impact+expl), 10))
		} else {
			score = roundUp(math.Min(impact+expl, 10))
		}
	}
	return score, Severity(score), nil
}

func roundUp(x float64) float64 {
	i := int(math.Round(x * 100000))
	if i%10000 == 0 {
		return float64(i) / 100000
	}
	return (math.Floor(float64(i)/10000) + 1) / 10
}

func Severity(score float64) string {
	switch {
	case score == 0:
		return "info"
	case score < 4:
		return "low"
	case score < 7:
		return "medium"
	case score < 9:
		return "high"
	}
	return "critical"
}

// SeverityRank orders severities for sorting.
func SeverityRank(s string) int {
	return map[string]int{"critical": 5, "high": 4, "medium": 3, "low": 2, "info": 1}[strings.ToLower(s)]
}
