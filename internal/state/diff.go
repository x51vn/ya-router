package state

import (
	"encoding/json"
	"reflect"
	"sort"

	configschema "github.com/duvu/ya-router/internal/config"
)

func diffPaths(before, after *configschema.Config) ([]string, error) {
	left, err := toMap(before)
	if err != nil {
		return nil, err
	}
	right, err := toMap(after)
	if err != nil {
		return nil, err
	}
	var paths []string
	walkDiff("", left, right, &paths)
	sort.Strings(paths)
	return paths, nil
}
func toMap(value any) (map[string]any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}
func walkDiff(prefix string, left, right any, paths *[]string) {
	if reflect.DeepEqual(left, right) {
		return
	}
	leftMap, lok := left.(map[string]any)
	rightMap, rok := right.(map[string]any)
	if lok && rok {
		keys := make(map[string]struct{}, len(leftMap)+len(rightMap))
		for key := range leftMap {
			keys[key] = struct{}{}
		}
		for key := range rightMap {
			keys[key] = struct{}{}
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			walkDiff(next, leftMap[key], rightMap[key], paths)
		}
		return
	}
	if prefix == "" {
		prefix = "$"
	}
	*paths = append(*paths, prefix)
}
