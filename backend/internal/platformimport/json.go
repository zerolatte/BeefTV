//go:build platform

package platformimport

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// 审计不能采用 JSON 重复键的后值覆盖语义，否则较早的凭据或资源引用会被忽略。
func strictJSON(raw string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("JSON 含额外内容")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 100 {
		return nil, errors.New("JSON 嵌套超出导入审计范围")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		value := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("JSON 对象键无效")
			}
			if _, exists := value[key]; exists {
				return nil, errors.New("JSON 包含重复键")
			}
			child, err := decodeValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			value[key] = child
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return value, nil
	case '[':
		value := []any{}
		for decoder.More() {
			child, err := decodeValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			value = append(value, child)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return value, nil
	default:
		return nil, errors.New("JSON 结束符无效")
	}
}
