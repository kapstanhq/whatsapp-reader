package main

import "runtime/debug"

// versao vem do build de release: -ldflags "-X main.versao=0.2.0". Compilado à
// mão fica "dev" — a não ser que o Go tenha gravado a versão do módulo no
// binário (`go install ...@v0.2.0`, ou um build dentro do repositório), e aí é
// de lá que ela vem.
var versao = "dev"

func versaoAtual() string {
	if versao != "dev" {
		return versao
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return versao
}
