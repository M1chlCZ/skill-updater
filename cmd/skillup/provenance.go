package main

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	githubCLI   = "GitHub CLI"
	skildCLI    = "Skild"
	skillsLock  = "Skills lock"
	gitCheckout = "Git checkout"
)

// Read the install marker before considering the shared lock or a linked checkout.
func (s *skill) readInstallerMetadata() {
	marker := filepath.Join(s.path, ".skild", "install.json")
	if info, err := os.Stat(marker); err == nil {
		s.manager, s.source, s.current = skildCLI, "", ""
		s.mark(skipped, "Use skild update")
		var record struct {
			SchemaVersion int
			Source        string
		}
		if info.Mode().IsRegular() && json.Unmarshal(readMetadata(marker), &record) == nil && record.SchemaVersion == 1 {
			s.source = record.Source
			if address, err := url.Parse(record.Source); err == nil && address.Host != "" {
				address.User, address.RawQuery, address.ForceQuery = nil, "", false
				s.source = address.String()
			}
		}
		return
	}

	var skillFile struct {
		Metadata struct {
			Repo  string `yaml:"github-repo"`
			Path  string `yaml:"github-path"`
			Tree  string `yaml:"github-tree-sha"`
			Pin   string `yaml:"github-pinned"`
			Local string `yaml:"local-path"`
		}
	}
	data := bytes.TrimLeft(readMetadata(filepath.Join(s.path, "SKILL.md")), "\r\n")
	first, rest, _ := bytes.Cut(data, []byte("\n"))
	if string(bytes.TrimSuffix(first, []byte("\r"))) != "---" {
		return
	}
	offset := 0
	for line := range bytes.SplitSeq(rest, []byte("\n")) {
		if string(bytes.TrimSuffix(line, []byte("\r"))) != "---" {
			offset += len(line) + 1
			continue
		}
		header := rest[:offset]
		if (!bytes.Contains(header, []byte("github-repo")) && !bytes.Contains(header, []byte("local-path"))) ||
			yaml.Unmarshal(header, &skillFile) != nil {
			return
		}
		break
	}
	meta := skillFile.Metadata
	repo, err := url.Parse(meta.Repo)
	if err == nil && repo.Scheme == "https" && repo.Host != "" && repo.User == nil && repo.RawQuery == "" &&
		repoName.MatchString(strings.TrimPrefix(repo.Path, "/")) && fs.ValidPath(meta.Path) && validOID(meta.Tree) {
		s.manager, s.source, s.current, s.githubPath = githubCLI, meta.Repo, meta.Tree, meta.Path
		s.mark(skipped, "Use gh skill update")
		if meta.Pin != "" {
			s.mark("Pinned", "Pinned by GitHub CLI")
		}
	} else if filepath.IsAbs(meta.Local) {
		s.manager, s.source, s.current = githubCLI, meta.Local, ""
		s.mark(skipped, "Use gh skill update")
	}
}

func readMetadata(filename string) []byte {
	file, err := os.Open(filename)
	if err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64*1024))
	if err != nil {
		return nil
	}
	return data
}

func (s *skill) managedExternally() bool {
	return s.manager == githubCLI || s.manager == skildCLI
}

func (s *skill) matchesGitHubLock(entry lockEntry) bool {
	if s.githubPath == "" || entry.SourceType != "github" || s.current != entry.SkillFolderHash {
		return false
	}
	source := strings.TrimSuffix(entry.SourceURL, ".git")
	if source == "" {
		source = "https://github.com/" + entry.Source
	}
	skillPath := entry.SkillPath
	if path.Base(skillPath) == "SKILL.md" {
		skillPath = path.Dir(skillPath)
	}
	return strings.EqualFold(s.source, source) && s.githubPath == skillPath
}
