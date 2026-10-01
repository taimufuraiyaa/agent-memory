package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
)

const localGraphEnvironmentFileMaxBytes = 64 << 10

func loadLocalGraphConfigFile(path string, graph config.GraphConfig) (config.GraphConfig, error) {
	if strings.TrimSpace(path) == "" {
		return graph, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return graph, nil
	}
	if err != nil {
		return graph, errors.New("local GraphRAG settings file is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > localGraphEnvironmentFileMaxBytes {
		return graph, errors.New("local GraphRAG settings file is invalid")
	}

	values := make(map[string]string, 5)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), localGraphEnvironmentFileMaxBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "export ") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, "export ")), "=")
		if !ok || !localGraphConfigKeys[key] {
			continue
		}
		if _, duplicate := values[key]; duplicate {
			return graph, errors.New("local GraphRAG settings file contains duplicate configuration")
		}
		value, err = localGraphEnvironmentValue(value)
		if err != nil {
			return graph, errors.New("local GraphRAG settings file contains an invalid value")
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return graph, errors.New("local GraphRAG settings file could not be read")
	}

	for key, value := range values {
		switch key {
		case "AGENT_MEMORY_GRAPH_ENABLED":
			switch value {
			case "true":
				graph.Enabled = true
			case "false":
				graph.Enabled = false
			default:
				return graph, errors.New("local GraphRAG enablement value is invalid")
			}
		case "AGENT_MEMORY_GRAPH_COMPLETION_PROVIDER":
			graph.CompletionProvider = value
		case "AGENT_MEMORY_GRAPH_COMPLETION_MODEL":
			graph.CompletionModel = value
		case "AGENT_MEMORY_GRAPH_EMBEDDING_PROVIDER":
			graph.EmbeddingProvider = value
		case "AGENT_MEMORY_GRAPH_EMBEDDING_MODEL":
			graph.EmbeddingModel = value
		}
	}
	return graph, nil
}

var localGraphConfigKeys = map[string]bool{
	"AGENT_MEMORY_GRAPH_ENABLED":             true,
	"AGENT_MEMORY_GRAPH_COMPLETION_PROVIDER": true,
	"AGENT_MEMORY_GRAPH_COMPLETION_MODEL":    true,
	"AGENT_MEMORY_GRAPH_EMBEDDING_PROVIDER":  true,
	"AGENT_MEMORY_GRAPH_EMBEDDING_MODEL":     true,
}

func localGraphEnvironmentValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "\"") {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", err
		}
		value = unquoted
	} else if strings.HasPrefix(value, "'") {
		if len(value) < 2 || value[len(value)-1] != '\'' || strings.Contains(value[1:len(value)-1], "'") {
			return "", fmt.Errorf("invalid single-quoted value")
		}
		value = value[1 : len(value)-1]
	}
	if value == "" || len(value) > 128 {
		return "", fmt.Errorf("value is outside policy")
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._:/+-", character)) {
			return "", fmt.Errorf("value contains an unsupported character")
		}
	}
	return value, nil
}
