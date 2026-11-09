package redis

import (
	"reflect"
	"testing"
)

func TestDBIndex(t *testing.T) {
	cases := map[string]int{"": 0, "0": 0, "db3": 3, "DB12": 12, "15": 15}
	for input, want := range cases {
		got, err := dbIndex(input)
		if err != nil || got != want {
			t.Fatalf("dbIndex(%q) = %d, %v；期望 %d", input, got, err, want)
		}
	}
	for _, input := range []string{"abc", "db-1", "1024"} {
		if _, err := dbIndex(input); err == nil {
			t.Fatalf("dbIndex(%q) 应当报错", input)
		}
	}
}

func TestSplitCommand(t *testing.T) {
	args, err := splitCommand(`SET user:1 "hello world" EX 60`)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"SET", "user:1", "hello world", "EX", "60"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("拆分结果 %#v，期望 %#v", args, want)
	}
	args, err = splitCommand(`HSET 'my key' field 'a b'`)
	if err != nil {
		t.Fatal(err)
	}
	want = []any{"HSET", "my key", "field", "a b"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("单引号拆分结果 %#v，期望 %#v", args, want)
	}
	for _, text := range []string{"", "   ", `SET k "unterminated`} {
		if _, err := splitCommand(text); err == nil {
			t.Fatalf("splitCommand(%q) 应当报错", text)
		}
	}
}

func TestIsWriteCommand(t *testing.T) {
	if !isWriteCommand([]any{"set", "k", "v"}) {
		t.Fatal("SET 应识别为写命令")
	}
	if isWriteCommand([]any{"GET", "k"}) || isWriteCommand([]any{"SCAN", "0"}) {
		t.Fatal("只读命令不应识别为写命令")
	}
}

func TestFormatValue(t *testing.T) {
	if got := formatValue([]any{"a", int64(2), nil}); got != "a\n2\n(nil)" {
		t.Fatalf("数组格式化错误: %q", got)
	}
	if got := formatValue(nil); got != "(nil)" {
		t.Fatalf("NULL 格式化错误: %q", got)
	}
}
