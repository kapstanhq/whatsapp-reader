package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

/* O que se testa aqui é o que RECUSA. O caminho feliz depende de uma conta
   pareada e de um servidor da Meta do outro lado; as travas, não — e são elas
   que separam esta ponte de um disparador. */

func jid(t *testing.T, s string) types.JID {
	t.Helper()
	j, err := types.ParseJID(s)
	if err != nil {
		t.Fatalf("jid %q: %v", s, err)
	}
	return j
}

func TestDestinoRecusaOQueNaoEUmaPessoa(t *testing.T) {
	casos := []struct {
		nome, entrada string
		aceita        bool
	}{
		{"pessoa", "5551999998888@s.whatsapp.net", true},
		{"grupo", "120363012345678901@g.us", false},
		{"transmissão", "5551999998888@broadcast", false},
		{"status", "status@broadcast", false},
		{"canal", "120363012345678901@newsletter", false},
	}
	for _, c := range casos {
		_, err := destino(c.entrada)
		if c.aceita && err != nil {
			t.Errorf("%s: devia aceitar, recusou com %v", c.nome, err)
		}
		if !c.aceita && err == nil {
			t.Errorf("%s: devia recusar e aceitou", c.nome)
		}
	}
}

func TestSoDigitosIgnoraAFormaDoJid(t *testing.T) {
	casos := map[string]string{
		"5551999998888":                   "5551999998888",
		"+55 51 99999-8888":               "5551999998888",
		"5551999998888@s.whatsapp.net":    "5551999998888",
		"5551999998888:12@s.whatsapp.net": "5551999998888",
	}
	for entrada, esperado := range casos {
		if s := soDigitos(entrada); s != esperado {
			t.Errorf("soDigitos(%q) = %q, esperava %q", entrada, s, esperado)
		}
	}
}

func TestPediuSilencio(t *testing.T) {
	dir := t.TempDir()
	lista := "# quem pediu para não receber mais\n" +
		"5551999998888 · pediu em 12/08\n" +
		"\n" +
		"+55 51 98888-7777\n"
	if err := os.WriteFile(filepath.Join(dir, arquivoSilencio), []byte(lista), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &Elo{dir: dir}

	if calou, motivo := e.pediuSilencio(jid(t, "5551999998888@s.whatsapp.net")); !calou {
		t.Error("devia estar na lista")
	} else if motivo != "pediu em 12/08" {
		t.Errorf("motivo = %q", motivo)
	}
	// O mesmo contato pela outra forma de jid.
	if calou, _ := e.pediuSilencio(jid(t, "5551988887777@s.whatsapp.net")); !calou {
		t.Error("número com máscara devia bater")
	}
	if calou, _ := e.pediuSilencio(jid(t, "5551977776666@s.whatsapp.net")); calou {
		t.Error("quem não está na lista não pode ser barrado")
	}
	// Sem arquivo nenhum, ninguém é barrado — é o caso comum.
	if calou, _ := (&Elo{dir: t.TempDir()}).pediuSilencio(jid(t, "5551999998888@s.whatsapp.net")); calou {
		t.Error("sem lista, ninguém é barrado")
	}
}

func TestRecebidaDesdeMataAPreviaVelha(t *testing.T) {
	b, err := AbrirBanco(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Fechar()
	ctx := context.Background()
	conversa := "5551999998888@s.whatsapp.net"
	previaEm := time.Now().Add(-5 * time.Minute)

	// Nada chegou: a prévia continua de pé.
	if nova, _, err := b.RecebidaDesde(ctx, conversa, previaEm); err != nil || nova {
		t.Fatalf("sem mensagem nova: nova=%v err=%v", nova, err)
	}

	// O CORRETOR escrevendo não mata a prévia — ele pode ter mandado o "oi".
	if err := b.GravarMensagem(ctx, Mensagem{
		ID: "a1", Conversa: conversa, DeMim: true, Texto: "oi", Em: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if nova, _, _ := b.RecebidaDesde(ctx, conversa, previaEm); nova {
		t.Error("mensagem do próprio corretor não pode matar a prévia")
	}

	// O CLIENTE escrevendo mata.
	if err := b.GravarMensagem(ctx, Mensagem{
		ID: "b2", Conversa: conversa, DeMim: false, Texto: "opa, e aí?", Em: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	nova, quando, err := b.RecebidaDesde(ctx, conversa, previaEm)
	if err != nil || !nova {
		t.Fatalf("mensagem do cliente devia matar a prévia: nova=%v err=%v", nova, err)
	}
	if quando.IsZero() {
		t.Error("a hora da mensagem nova precisa voltar, é o que a recusa mostra")
	}
}

func TestCodigoPreviaEvitaAmbiguidade(t *testing.T) {
	for i := 0; i < 200; i++ {
		c := codigoPrevia()
		if len(c) != 4 {
			t.Fatalf("código %q tem %d caracteres", c, len(c))
		}
		for _, r := range c {
			// Ele é lido em voz alta: "manda a 7Q4K".
			if strings.ContainsRune("IOSB0125", r) {
				t.Fatalf("código %q tem caractere ambíguo %q", c, r)
			}
		}
	}
}
