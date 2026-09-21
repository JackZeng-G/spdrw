package app

import (
	"reflect"
	"strings"
	"testing"
)

// Wails v2 绑定契约的机械护栏。
//
// 本项目两次被同一类问题咬过:
//   - internal/binding/boundMethod.go 的 Call 只处理 OutputCount 1/2, 3 个以上返回值
//     会被静默序列化成 null(前端拿到 null 再解构就抛错), WPStatus/AutoConnectAll 都栽过
//   - 参数经 json.Unmarshal 解码, JS 数组解不进 []byte([]byte 只接受 base64 字符串)
//
// 返回值数量可以由反射机械校验, 于是把它变成测试: 以后任何人加了一个 3 返回值的方法,
// 这里立刻失败, 而不是等真机上界面"点了没反应"。
func TestWailsBindingContract(t *testing.T) {
	appType := reflect.TypeOf(&App{})
	const maxOutputs = 2 // Wails v2 internal/binding/boundMethod.go 的硬限制
	checked := 0
	for i := 0; i < appType.NumMethod(); i++ {
		m := appType.Method(i)
		if m.PkgPath != "" { // 非导出方法不会被绑定
			continue
		}
		// 只校验签名形如 func(*App, ...) ... 的方法
		if m.Type.NumIn() < 1 || m.Type.In(0) != appType {
			continue
		}
		checked++
		if n := m.Type.NumOut(); n > maxOutputs {
			t.Errorf("绑定方法 %s 有 %d 个返回值(上限 %d): Wails v2 会静默返回 null, "+
				"请打包成单个结构体", m.Name, n, maxOutputs)
		}
		// 最后一个返回值若是 error, 必须正好是 error 类型(否则 Wails 不认)
		if n := m.Type.NumOut(); n > 0 && m.Type.Out(n-1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			if m.Type.Out(n-1) != reflect.TypeOf((*error)(nil)).Elem() {
				t.Errorf("绑定方法 %s 的最后一个返回值是 %s, 应为 error", m.Name, m.Type.Out(n-1))
			}
		}
	}
	if checked < 20 {
		t.Fatalf("只检查到 %d 个绑定方法, 反射可能没走对", checked)
	}
	t.Logf("绑定方法签名检查: %d 个方法全部满足 ≤%d 返回值", checked, maxOutputs)
}

// TestNoByteSliceParamsForArrayArgs 记录一条易踩的约定:
// []byte 参数只能从 base64 字符串解出, 前端传数字数组时必须用 []int 之类的类型。
// 目前唯一接收 JS 数字数组的绑定方法是 WPSet, 这里把它钉死。
func TestNoByteSliceParamsForArrayArgs(t *testing.T) {
	appType := reflect.TypeOf(&App{})
	byteSlice := reflect.TypeOf([]byte{})
	for i := 0; i < appType.NumMethod(); i++ {
		m := appType.Method(i)
		if m.PkgPath != "" {
			continue
		}
		for a := 1; a < m.Type.NumIn(); a++ {
			in := m.Type.In(a)
			if in != byteSlice {
				continue
			}
			// []byte 参数只允许出现在"二进制走 base64"的语义里
			allowed := map[string]bool{
				"Decode": true, // 前端传 base64 字符串(唯一)
			}
			if !allowed[m.Name] {
				t.Errorf("绑定方法 %s 的参数 %d 是 []byte: 若前端传的是数字数组会解码失败,"+
					"请改用 []int/[]string 或明确走 base64 字符串", m.Name, a)
			}
		}
	}
	if _, ok := appType.MethodByName("WPSet"); !ok {
		t.Fatal("WPSet 应存在")
	}
	m, _ := appType.MethodByName("WPSet")
	if got := m.Type.In(1).String(); got != "[]int" {
		t.Fatalf("WPSet 的参数类型 = %s, 期望 []int(JS 数组可解)", got)
	}
	if n := m.Type.NumOut(); n != 1 {
		t.Fatalf("WPSet 应只有 1 个返回值, got %d", n)
	}
	if !strings.Contains(m.Type.Out(0).String(), "error") {
		t.Fatalf("WPSet 的返回值应为 error, got %s", m.Type.Out(0))
	}
}
