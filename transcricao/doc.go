/*
Package transcricao é o contrato entre quem tem um áudio e quem o transforma em
texto, e nada além disso: sem fila, sem banco, sem WhatsApp.

Um [Motor] recebe um [Audio] que já está no disco e um [Pedido] — o idioma e
uma dica de vocabulário — e devolve um [Resultado]. Texto vazio é sucesso: o
áudio não tinha fala reconhecível, e repetir não muda isso.

Quem chama não precisa conhecer os erros de cada motor. [Classificar] reduz
qualquer erro a uma [Classe], que diz o que fazer em seguida:

	Passageiro    tente este áudio de novo mais tarde
	Definitivo    este áudio não vai dar; siga com os outros
	Configuracao  nenhum vai dar até alguém instalar algo ou trocar a chave
	Cancelado     quem chamou desistiu; não conte a tentativa

Quando um serviço pede para esperar, o erro traz um [ErroLimite] com o tempo.

Os motores vivem em subpacotes, um por produto:

	transcricao/whispercpp  whisper.cpp local, chamando a whisper-cli
	transcricao/openai      API compatível com a da OpenAI (OpenAI, Groq, Speaches)
	transcricao/ffmpeg      converte o áudio para o WAV que os motores locais leem
	transcricao/vocabulario a dica de nomes que vai junto e a correção do que volta

Motor novo é subpacote novo; nada aqui muda. Este pacote e os subpacotes
importam só a biblioteca padrão — os motores chamam programas externos ou HTTP
— e compilam com CGO_ENABLED=0. O CI confere.

Estabilidade: v0. A API pode mudar entre versões menores até a v1.

In English: package transcricao defines a small speech-to-text contract (Motor,
Audio, Pedido, Resultado) and an error classification (Classificar) that tells
a job queue whether to retry later, give up on one file, stop for
configuration, or treat the error as a cancellation. Engines live in
subpackages. Identifiers and messages are in Portuguese, like the rest of
whatsapp-reader.
*/
package transcricao
