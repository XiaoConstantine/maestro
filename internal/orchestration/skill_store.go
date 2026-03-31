package orchestration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
)

const defaultPersistedSkillStoreFile = "skills.json"

func resolveSkillStorePath(path, envVar, memoryPath, fallbackPath string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(envVar))
	}
	if path == "" {
		path = strings.TrimSpace(fallbackPath)
	}
	if path == "" {
		stateDir, err := resolveMaestroStateDir(memoryPath)
		if err != nil {
			return "", err
		}
		path = filepath.Join(stateDir, defaultPersistedSkillStoreFile)
	}
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for persisted skill store: %w", err)
		}
		path = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Clean(path), nil
}

func resolveSkillDomain(domain, envVar, defaultDomain string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = strings.TrimSpace(os.Getenv(envVar))
	}
	if domain == "" {
		return strings.TrimSpace(defaultDomain)
	}
	return domain
}

func resolveMaestroStateDir(memoryPath string) (string, error) {
	memoryPath = strings.TrimSpace(memoryPath)
	if memoryPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for Maestro state: %w", err)
		}
		return filepath.Join(homeDir, ".maestro"), nil
	}
	if strings.HasPrefix(memoryPath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for Maestro state: %w", err)
		}
		memoryPath = filepath.Join(homeDir, strings.TrimPrefix(memoryPath, "~/"))
	}
	cleaned := filepath.Clean(memoryPath)
	if strings.HasSuffix(memoryPath, string(os.PathSeparator)) || filepath.Ext(cleaned) == "" {
		return cleaned, nil
	}
	return filepath.Dir(cleaned), nil
}

func loadBestSkill(ctx context.Context, store skills.Store, domain string) (*skills.Skill, error) {
	if store == nil {
		return nil, nil
	}
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil, nil
	}
	return store.Best(ctx, domain)
}
