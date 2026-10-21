package dispatch

import (
	"fmt"
	"strconv"
	"strings"
)

const namePrefix = "hub-"

// RunnerName names a runner, its container and its work dir after the box
// and the job it was started for, so a restarted dispatcher can tell which
// jobs already have a runner coming.
func RunnerName(box string, jobID int64) string {
	return fmt.Sprintf("%s%s-%d", namePrefix, box, jobID)
}

func ParseRunnerName(name string) (box string, jobID int64, ok bool) {
	rest, found := strings.CutPrefix(name, namePrefix)
	if !found {
		return "", 0, false
	}
	i := strings.LastIndexByte(rest, '-')
	if i <= 0 {
		return "", 0, false
	}
	id, err := strconv.ParseInt(rest[i+1:], 10, 64)
	if err != nil || id <= 0 {
		return "", 0, false
	}
	return rest[:i], id, true
}

// RequiredArch reads a job's runs-on labels. ok is false for jobs the
// dispatcher must leave alone: GitHub-hosted ones and non-Linux ones. arch
// is "" when any arch will do.
func RequiredArch(labels []string) (arch string, ok bool) {
	selfHosted := false
	for _, l := range labels {
		switch u := strings.ToUpper(l); u {
		case "SELF-HOSTED":
			selfHosted = true
		case "WINDOWS", "MACOS":
			return "", false
		case "X64", "ARM64", "ARM":
			if arch != "" && arch != u {
				return "", false
			}
			arch = u
		}
	}
	return arch, selfHosted
}

// RunnerLabels is the job's labels plus the defaults a registered runner
// would carry, without case-insensitive duplicates. GitHub matches labels
// case-insensitively, and the JIT endpoint adds none of its own.
func RunnerLabels(jobLabels []string, boxArch string) []string {
	out := []string{"self-hosted", "Linux", boxArch}
	seen := map[string]bool{}
	for _, l := range out {
		seen[strings.ToLower(l)] = true
	}
	for _, l := range jobLabels {
		if !seen[strings.ToLower(l)] {
			seen[strings.ToLower(l)] = true
			out = append(out, l)
		}
	}
	return out
}
