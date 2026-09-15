package transcricao

import (
	"context"
	"time"
)

// Audio é um arquivo de áudio que já está no disco.
type Audio struct {
	Caminho string
	// Mime como veio do remetente ("audio/ogg; codecs=opus"); vazio se não se sabe.
	Mime string
	// Duracao declarada pelo remetente; zero se não se sabe. Motores locais a
	// usam para decidir quanto esperar antes de desistir do processo.
	Duracao time.Duration
}

// Pedido é o que quem chama sabe sobre o áudio e o motor não adivinharia.
type Pedido struct {
	// Idioma em ISO 639-1 ("pt"). Vazio deixa o motor detectar — e nota de voz
	// curta, detectada, costuma sair em outro idioma.
	Idioma string
	// Dica é vocabulário que costuma aparecer: nomes próprios, termos do
	// assunto. Motores que não aceitam dica a ignoram.
	Dica string
}

// Resultado de uma transcrição.
type Resultado struct {
	// Texto vazio é sucesso: o áudio não tinha fala reconhecível.
	Texto string
	// Idioma que o motor usou ou detectou.
	Idioma string
	Motor  string // "whisper.cpp", "openai"
	Modelo string
	Levou  time.Duration
}

// Motor transforma um áudio em texto. Os erros embrulham os sentinelas deste
// pacote, para que [Classificar] saiba o que fazer com eles.
type Motor interface {
	Transcrever(ctx context.Context, a Audio, p Pedido) (Resultado, error)
}

// Verificador confere, sem transcrever nada, se o motor tem o que precisa:
// executável, modelo, credencial. Barato o bastante para rodar antes de cada
// lote.
type Verificador interface {
	Verificar(ctx context.Context) error
}

// Conversor transforma um áudio qualquer no WAV PCM de 16 kHz mono que os
// motores locais leem.
type Conversor interface {
	ParaWAV(ctx context.Context, origem, destino string) error
}
