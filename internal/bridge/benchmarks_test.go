package bridge

import (
	"testing"
)

// Benchmarks de hot paths del bridge — funciones que corren por cada
// mensaje incoming. Útiles para detectar regresiones de performance
// al cambiar el template, los parsers, etc. v0.62.0.
//
// Run con:
//
//	go test -bench=. -benchmem -run=^$ ./internal/bridge/...
//
// La regla `-run=^$` salta los tests funcionales — sólo ejecuta
// benchmarks. -benchmem añade allocs/op a la salida.

// BenchmarkRenderSenderHeader_Default mide el path más caliente:
// render del template con todos los tokens presentes y saved=true.
func BenchmarkRenderSenderHeader_Default(b *testing.B) {
	si := senderInfo{
		phone:    "34604021705",
		phoneFmt: "+34604021705",
		name:     "Ricard Penín",
		saved:    true,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = renderSenderHeader(si, "")
	}
}

// BenchmarkRenderSenderHeader_CustomTemplate fuerza el path donde
// el template viene del config y no del default. Excite el parser
// de tokens, no sólo la concatenación.
func BenchmarkRenderSenderHeader_CustomTemplate(b *testing.B) {
	si := senderInfo{
		phone:    "34604021705",
		phoneFmt: "+34604021705",
		name:     "Ricard Penín",
		saved:    true,
	}
	const tmpl = "**$name** _($phone)_"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = renderSenderHeader(si, tmpl)
	}
}

// BenchmarkRenderSenderHeader_UnsavedTilde mide el path con
// saved=false (añade el `~` al nombre — branch ligeramente distinto).
func BenchmarkRenderSenderHeader_UnsavedTilde(b *testing.B) {
	si := senderInfo{
		phone:    "34604021705",
		phoneFmt: "+34604021705",
		name:     "Richard",
		saved:    false,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = renderSenderHeader(si, "")
	}
}

// BenchmarkResolveMentions_NoMentions: caso de body con texto sin
// @-mentions. Es el caso más común — el coste de la función debería
// aproximarse a 0 (early-return).
func BenchmarkResolveMentions_NoMentions(b *testing.B) {
	body := "Hola Ricard, ¿qué tal va el proyecto? Te llamo mañana."
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = resolveMentions(body, nil, nil, "@$name")
	}
}
