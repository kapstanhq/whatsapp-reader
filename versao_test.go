package main

import "testing"

func TestVersaoDoBuildVenceADoModulo(t *testing.T) {
	antes := versao
	t.Cleanup(func() { versao = antes })

	versao = "0.2.0"
	if v := versaoAtual(); v != "0.2.0" {
		t.Errorf("com -X main.versao=0.2.0, versaoAtual() = %q", v)
	}
	versao = "dev"
	if v := versaoAtual(); v == "" {
		t.Error("sem ldflags a versão não pode sair vazia")
	}
}
