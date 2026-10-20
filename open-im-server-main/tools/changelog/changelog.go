package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// You can specify a tag as a command line argument to generate the changelog for a specific version.
// 示例：go run tools/changelog/changelog.go v0.0.33
// 不传 tag 时默认使用最新的 release。

// 生成 changelog 前需设置仓库所有者与仓库名
const (
	repoOwner = "openimsdk"
	repoName  = "open-im-server"
)

// GitHubRepo 结构体保存仓库的基本信息。
type GitHubRepo struct {
	Owner         string
	Repo          string
	FullChangelog string
}

// ReleaseData represents the JSON structure for release data.
type ReleaseData struct {
	TagName   string `json:"tag_name"`
	Body      string `json:"body"`
	HtmlUrl   string `json:"html_url"`
	Published string `json:"published_at"`
}

type GitHubError struct {
	Message string `json:"message"`
}

// 对 release note 进行分类并格式化。
func (g *GitHubRepo) classifyReleaseNotes(body string) map[string][]string {
	result := map[string][]string{
		"feat":     {},
		"fix":      {},
		"chore":    {},
		"refactor": {},
		"build":    {},
		"other":    {},
	}

	// Regular expression to extract PR number and URL (case insensitive)
	rePR := regexp.MustCompile(`(?i)in (https://github\.com/[^\s]+/pull/(\d+))`)

	for line := range strings.SplitSeq(body, "\n") {
		// 跳过包含 "deps: Merge" 的行
		if strings.Contains(strings.ToLower(line), "deps: merge #") {
			continue
		}

		// Use a regular expression to extract Full Changelog link and its title (case insensitive).
		if strings.Contains(strings.ToLower(line), "**full changelog**") {
			matches := regexp.MustCompile(`(?i)\*\*full changelog\*\*: (https://github\.com/[^\s]+/compare/([^\s]+))`).FindStringSubmatch(line)
			if len(matches) > 2 {
				// 格式化 Full Changelog 链接及其标题
				g.FullChangelog = fmt.Sprintf("[%s](%s)", matches[2], matches[1])
			}
			continue // Skip further processing for this line.
		}

		if strings.HasPrefix(line, "*") {
			var category string

			// Use strings.ToLower to make the matching case insensitive
			lowerLine := strings.ToLower(line)

			// Determine the category based on the prefix (case insensitive).
			if strings.HasPrefix(lowerLine, "* feat") {
				category = "feat"
			} else if strings.HasPrefix(lowerLine, "* fix") {
				category = "fix"
			} else if strings.HasPrefix(lowerLine, "* chore") {
				category = "chore"
			} else if strings.HasPrefix(lowerLine, "* refactor") {
				category = "refactor"
			} else if strings.HasPrefix(lowerLine, "* build") {
				category = "build"
			} else {
				category = "other"
			}

			// Extract PR number and URL (case insensitive)
			matches := rePR.FindStringSubmatch(line)
			if len(matches) == 3 {
				prURL := matches[1]
				prNumber := matches[2]
				// Format the line with the PR link and use original content for the final result
				formattedLine := fmt.Sprintf("* %s [#%s](%s)", strings.Split(line, " by ")[0][2:], prNumber, prURL)
				result[category] = append(result[category], formattedLine)
			} else {
				// 未找到 PR 链接时，原样添加该行
				result[category] = append(result[category], line)
			}
		}
	}

	return result
}

// 生成最终的 changelog。
func (g *GitHubRepo) generateChangelog(tag, date, htmlURL, body string) (string, error) {
	if tag == "" {
		return "", errors.New("release tag is empty")
	}
	if len(date) < 10 {
		return "", fmt.Errorf("release published_at is invalid: %q", date)
	}
	if htmlURL == "" {
		return "", errors.New("release html_url is empty")
	}

	sections := g.classifyReleaseNotes(body)

	// 将 ISO 8601 日期转换为 YYYY-MM-DD 格式
	formattedDate := date[:10]

	// changelog 头部，包含 tag、日期和链接。
	changelog := fmt.Sprintf("## [%s](%s) \t(%s)\n\n", tag, htmlURL, formattedDate)

	if len(sections["feat"]) > 0 {
		changelog += "### New Features\n" + strings.Join(sections["feat"], "\n") + "\n\n"
	}
	if len(sections["fix"]) > 0 {
		changelog += "### Bug Fixes\n" + strings.Join(sections["fix"], "\n") + "\n\n"
	}
	if len(sections["chore"]) > 0 {
		changelog += "### Chores\n" + strings.Join(sections["chore"], "\n") + "\n\n"
	}
	if len(sections["refactor"]) > 0 {
		changelog += "### Refactors\n" + strings.Join(sections["refactor"], "\n") + "\n\n"
	}
	if len(sections["build"]) > 0 {
		changelog += "### Builds\n" + strings.Join(sections["build"], "\n") + "\n\n"
	}
	if len(sections["other"]) > 0 {
		changelog += "### Others\n" + strings.Join(sections["other"], "\n") + "\n\n"
	}

	if g.FullChangelog != "" {
		changelog += fmt.Sprintf("**Full Changelog**: %s\n", g.FullChangelog)
	}

	return changelog, nil
}

// 从 GitHub API 获取 release 数据。
func (g *GitHubRepo) fetchReleaseData(version string) (*ReleaseData, error) {
	var apiURL string

	if version == "" {
		// 获取最新的 release。
		apiURL = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", g.Owner, g.Repo)
	} else {
		// 获取指定版本。
		apiURL = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", g.Owner, g.Repo, version)
	}

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var githubError GitHubError
		if err := json.Unmarshal(body, &githubError); err == nil && githubError.Message != "" {
			return nil, fmt.Errorf("github api returned %s: %s", resp.Status, githubError.Message)
		}
		return nil, fmt.Errorf("github api returned %s", resp.Status)
	}

	var releaseData ReleaseData
	err = json.Unmarshal(body, &releaseData)
	if err != nil {
		return nil, err
	}

	return &releaseData, nil
}

func main() {
	repo := &GitHubRepo{Owner: repoOwner, Repo: repoName}

	// 从命令行参数中获取版本号（若已提供）
	var version string // Default is use latest

	if len(os.Args) > 1 {
		version = os.Args[1] // Use the provided version
	}

	// Fetch release data (either for latest or specific version)
	releaseData, err := repo.fetchReleaseData(version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error fetching release data:", err)
		os.Exit(1)
	}

	// 生成并打印格式化后的 changelog
	changelog, err := repo.generateChangelog(releaseData.TagName, releaseData.Published, releaseData.HtmlUrl, releaseData.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error generating changelog:", err)
		os.Exit(1)
	}
	fmt.Println(changelog)
}
