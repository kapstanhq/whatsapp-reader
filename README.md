# whatsapp-reader

**Lê as suas conversas do WhatsApp e as entrega a um agente por MCP. Manda uma
mensagem por vez, e só depois de você ver o texto e para quem vai.**

> *WhatsApp bridge exposing MCP tools. Reading is unrestricted; sending is one
> message at a time, gated on a preview the operator must be shown first. Bulk
> messaging is not a missing feature — it is refused by design and cannot be
> expressed by the API. Voice notes are downloaded and transcribed locally
> (whisper.cpp) or, opt-in, through an OpenAI-compatible API. Written for a
> Portuguese-language plugin; docs below are in Portuguese.*

---

## Não procure aqui o que ele não faz

Se você chegou querendo **disparar mensagem, automatizar atendimento ou responder
cliente sozinho**, este não é o projeto — e não é falta de tempo de implementar.

Enviar em massa é o que faz uma conta de WhatsApp ser bloqueada, e quem usa isto
atende do número pessoal, onde estão os clientes dele. Então o disparo não é
desencorajado: **ele não tem como ser expresso.** `enviar_mensagem` aceita **uma**
conversa — não uma lista —, exige o código de uma prévia que foi mostrada antes, e
recusa grupo, canal e lista de transmissão pelo tipo do destinatário.

O que sai daqui é sempre texto de uma pessoa para outra, com o nome de quem
recebe na tela antes de sair.

## Para que existe

Ele serve à [Oficina Kapstan](https://kapstan.com.br/oficina) — um pack de dez
skills para corretor de imóveis. Sem ele, o corretor cola a conversa na mão, e
tudo funciona igual. Com ele, o agente lê a conversa direto.

**O conector é opcional em todo o pack.** Nenhuma skill exige, e é o contrato
delas que garante isso.

## O que é, e o que não é

Não é fork de nada. Usa o [whatsmeow](https://github.com/tulir/whatsmeow), a
biblioteca que fala o protocolo do WhatsApp:

```
banco.go        esquema SQLite e as consultas
esquema.go      migrações: só aditivas, versionadas por PRAGMA user_version
servir.go       o daemon: conecta, pareia, captura histórico e mensagens
midias.go       a chave de cada anexo, guardada na hora em que a mensagem chega
esteira.go      quem baixa: fila no banco, espera crescente, pedido ao celular
transcrever.go  quem transcreve: um áudio por vez, e o que fazer com cada erro
motor.go        o único lugar que lê o ambiente para montar o motor
verificar.go    o subcomando que diz o que falta instalar
vocabulario.go  a dica de nomes: base, pacotes, pessoal e a conversa
limpeza.go      a retenção dos arquivos de áudio
rotulo.go       o que o agente lê de cada áudio
mcp.go          o protocolo MCP, JSON-RPC sobre stdio, escrito à mão
ponte.go        o elo entre os dois processos, e as travas do envio
envios.go       a tabela de envios: o que saiu, o que foi recusado, e por quê
cuidados.go     a lista de não contatar, a prévia que envelhece, a digitação
main.go         os subcomandos

midia/                   o anexo: extrair, baixar, pedir ao celular
transcricao/             o contrato de transcrição, só com a biblioteca padrão
transcricao/whispercpp/  whisper.cpp local
transcricao/openai/      API compatível com a da OpenAI
transcricao/ffmpeg/      a conversão para WAV
transcricao/vocabulario/ a dica de nomes e a correção do que volta
```

O MCP é escrito à mão porque são quatro métodos — `initialize`, `ping`,
`tools/list`, `tools/call`. Um SDK custaria mais que o protocolo inteiro.

### Por que o envio precisa de dois processos falando

O `serve` é quem tem a conexão viva; o `mcp` é curto, só lê o banco e morre com o
agente. Para o `mcp` mandar enviar, ele **pede** ao daemon, por HTTP em
`127.0.0.1`, com porta sorteada pelo sistema e um segredo por sessão gravado em
`elo.json`.

Não se abre um segundo cliente sobre o mesmo `sessao.db`: o estado do ratchet do
Signal é de **um** dispositivo, e dois clientes sobre ele corrompem a sessão — o
pareamento cai e o histórico não volta.

### O que o envio recusa

O primeiro contato tem uma recusa que **não é nossa**: desde **02/07/2026** o
WhatsApp responde **erro 463** a qualquer mensagem para quem nunca trocou
mensagem com a conta ([whatsmeow #1197](https://github.com/tulir/whatsmeow/issues/1197),
aberta). Salvar na agenda não resolve — a conversa só abre pelo aplicativo do
celular, e depois disso a ponte responde normalmente. A prévia diz isso ANTES,
quando vê que a conversa não existe, e o envio traduz o 463 se ele vier assim
mesmo. É o contrário de esconder: quem lê recebe o caminho, não um número.

```
mais de uma conversa por chamada     não existe como forma: é string, não lista
grupo, canal, lista de transmissão   recusados pelo TIPO do destinatário
texto ou destino diferentes da prévia    o que sai é o que foi mostrado
prévia vencida, usada duas vezes,
  ou com mensagem nova por cima      o cliente escreveu enquanto ela esperava
quem está em nao-contatar.txt        uma linha por pessoa, no diretório da ponte
acima do teto da hora                6 conversas distintas, 30 envios, 5 s entre
```

Os tetos são o formato certo com valor de partida arbitrário — a recusa diz o
número e onde mudá-lo. Toda recusa vira linha na tabela `envios`: sem isso a
trava é invisível para quem quiser auditá-la.

E antes de cada envio a ponte manda o indicador de digitação. Não é enfeite: a
Meta nomeia a **ausência** dele como sinal de robô, no white paper *Stopping
Abuse*.

## Compilar

Cada versão marcada (`v*`) publica binários prontos para Windows, macOS e Linux,
em amd64 e arm64, na página de releases. Para compilar, precisa de Go. **Não
precisa de compilador C**: o driver SQLite é `modernc.org/sqlite`, em Go puro.

```sh
git clone https://github.com/kapstanhq/whatsapp-reader
cd whatsapp-reader
CGO_ENABLED=0 go build -o whatsapp-reader .
```

Compilação cruzada funciona de qualquer máquina para qualquer alvo:

```sh
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o whatsapp-reader-mac .
```

## Usar

```sh
whatsapp-reader serve          # janela própria: mantém a conexão e grava
whatsapp-reader mcp            # o que o agente executa: só lê o banco
whatsapp-reader estado         # a ponte está de pé? conectada? parada desde quando?
whatsapp-reader nao-contatar   # quem não pode receber mensagem
whatsapp-reader verificar      # o que a transcrição de áudio precisa, e se está instalado
whatsapp-reader vocabulario    # os nomes que ajudam a transcrição, e de onde vêm
```

O `estado` sai com **1** quando a ponte não está no ar, para quem quiser chamá-lo
de um script. O `nao-contatar` sem argumento lista; com `<número> "<motivo>"`
põe; com `--tirar <número>` remove. Ele existe porque o arquivo mora no
diretório da ponte, e quem precisa escrever nele é uma skill que só conhece a
carteira — que pode estar no Google Drive, onde o daemon não chega.

**São dois processos, e a razão importa.** O `serve` precisa sobreviver ao
agente fechar: é ele que recebe as mensagens. Se a janela dele fechar, o
histórico congela no último momento em que ele esteve vivo, e **o que passou
enquanto ele esteve fora não volta**.

Isso não é hipótese. No 01/09/2026 o daemon desta máquina morreu às 11:05 e
ficou fora **seis dias** — e `estado_da_ponte` seguiu respondendo o número de
conversas, sem uma palavra sobre estar caído, porque lia só o banco. O banco de
um daemon morto tem a cara exata do banco de um dia quieto. É por isso que o
daemon agora **bate no banco a cada 30 s**, e é a batida velha que o `estado`
lê para dizer há quanto tempo ele se foi.

**Queda de rede ele resolve sozinho** — o whatsmeow nasce com `EnableAutoReconnect`
e força a volta se os pings falharem por três minutos. O que ele não resolve é o
processo deixar de existir (reboot, a janela fechada, um `kill`), nem o que exige
gente: sessão assumida por outro dispositivo, deslogada no celular, versão velha,
conta restringida. Cada um desses vira uma linha que o `estado` mostra, dizendo
qual dos dois casos é.

**Para ele voltar sozinho depois de um reboot**, o caminho é o agendador do
sistema — Tarefa Agendada no Windows (gatilho "ao fazer logon", ação
`whatsapp-reader serve`), ou um `launchd` com `KeepAlive` no macOS. Sem isso,
a disciplina de reabrir a janela é a única coisa entre a conta e mais uma semana
de silêncio, e ela já falhou uma vez aqui. No Windows, `schtasks /create` pode
responder *Acesso negado* sem elevação; um atalho para o binário, com `serve`
nos argumentos, dentro de `shell:startup`, faz o mesmo sem pedir nada.

**Uma ponte por máquina.** Com o daemon subindo sozinho no logon, abrir uma
janela e rodar `serve` vira o gesto natural — e dois clientes sobre o mesmo
`sessao.db` corrompem o ratchet do Signal: as mensagens passam a chegar sem
decifrar, e o console não diz nada. Então o segundo `serve` **recusa subir**
enquanto houver batida de menos de 90 s, nomeando o processo que está de pé.
Daemon que morreu de vez fica esses 90 s sem poder voltar, e a recusa diz isso.

Na primeira execução, o `serve` mostra um QR — no terminal e também em
`qr.png`, para quando o terminal não desenha os blocos. Escaneie em
*Configurações › Dispositivos conectados › Conectar dispositivo*. O QR some do
disco assim que serve: é credencial.

Registrar no Claude Code:

```sh
claude mcp add whatsapp -- /caminho/para/whatsapp-reader mcp
```

## As tools

| tool | o que devolve |
|---|---|
| `listar_conversas` | conversas por atividade, com contagem; filtra por nome ou telefone |
| `listar_mensagens` | mensagens por conversa, por período ou por texto — os áudios vêm transcritos, e a busca entra na transcrição |
| `ultima_interacao` | quando foi a última mensagem, de quem, e há quantos dias — e o que foi dito, se foi áudio |
| `estado_da_ponte` | a SAÚDE: de pé? conectada? parada desde quando? — mais o que está guardado, a fila dos áudios, o motor de transcrição e o número da conta |
| `preparar_envio` | MOSTRA a mensagem antes de ela sair: para quem, o nome, o texto |
| `enviar_mensagem` | envia o que a prévia mostrou — uma por vez, nunca em lote |

`ultima_interacao` existe porque uma das skills precisa saber há quantos dias
um cliente sumiu e de quem foi a última palavra. As tools servem as skills, e
não o contrário.

As duas últimas são um PAR, e a separação é o mecanismo: `preparar_envio` não
manda nada — devolve um código, o destinatário por nome (ou o telefone
formatado, quando não está na agenda), o texto exato e um aviso quando a pessoa
nunca respondeu. `enviar_mensagem` exige esse código e a REPETIÇÃO exata da
conversa e do texto; qualquer divergência recusa. A prévia vale 10 minutos,
serve uma vez só, e morre se o cliente escrever no meio-tempo — o texto foi
escrito para a conversa como ela estava.

O que a ponte recusa, e é ERRO, não tutela: grupo, lista de transmissão e canal
(só pessoa); quem está no `nao-contatar.txt`; e a mesma mensagem para a mesma
pessoa dentro de um minuto, que é dedo duplo e não decisão. Passado o minuto a
prévia AVISA que já saiu, e quem decide é quem está na frente da tela.

## Áudios

Nota de voz de conversa individual é baixada e transcrita sozinha, sem atrasar o
recebimento, e chega ao agente assim:

```
[2026-09-15 14:01] seu João: [áudio 0:42 · transcrição] olha, amanhã eu passo aí para ver as calhas
```

A marca `transcrição` fica de propósito: é texto de máquina, e troca nome,
número e valor. Áudio sem transcrição diz por quê — `na fila para transcrever`,
`não baixado: grupo`, `o arquivo expirou e o celular não tem mais` — em vez de
um `[áudio]` que não distingue "ainda vai" de "nunca vai".

**Por que existe.** Até a v0.1 a ponte gravava só o rótulo e jogava fora a
chave do anexo. O WhatsApp entrega o histórico uma vez, com a chave junto; em
15/09/2026 os áudios do pedreiro sobre a reforma das calhas ficaram ilegíveis
por isso. Agora a chave é guardada na hora em que a mensagem chega. Os áudios
recebidos antes desta versão continuam só com o rótulo: a chave deles já se foi.

**O que entra na fila:** áudio de conversa individual dos últimos 7 dias
(`WHATSAPP_READER_MIDIA_DIAS`). Grupo e áudio mais antigo têm a chave guardada,
mas não são baixados. Visualização única não guarda nem a chave — quem mandou
escolheu que não ficasse.

### O motor local (padrão)

Nada sai da máquina. São dois programas e um modelo:

```sh
scoop install whisper-cpp ffmpeg     # Windows
brew install whisper-cpp ffmpeg      # macOS
```

No Linux, o ffmpeg vem do gerenciador da distribuição e o whisper.cpp é
compilado de <https://github.com/ggml-org/whisper.cpp>.

O modelo vai em `modelos/`, no diretório da ponte, baixado de
<https://huggingface.co/ggerganov/whisper.cpp>. A ponte usa o primeiro que achar,
nesta ordem:

| modelo | tamanho | |
|---|---|---|
| `ggml-large-v3-turbo-q5_0.bin` | 574 MB | o preferido, mais preciso |
| `ggml-small-q5_1.bin` | 190 MB | para a máquina que não acompanha o primeiro |

Para saber o que falta, e se a sua máquina acompanha:

```sh
whatsapp-reader verificar            # peça por peça, com a dica de instalação
whatsapp-reader verificar nota.ogg   # transcreve e mede: "0:42 de áudio em 0:31 (0,7× a duração)"
```

Abaixo de 1× a fila anda; acima, acumula. A transcrição roda um áudio por vez,
com metade dos núcleos e, no Windows, em prioridade baixa: quem está usando o
computador não sente.

### Ou uma API (opcional)

```sh
WHATSAPP_READER_TRANSCRICAO=api
WHATSAPP_READER_API_URL=https://api.groq.com/openai/v1
WHATSAPP_READER_API_MODELO=whisper-large-v3-turbo
WHATSAPP_READER_API_CHAVE=...
```

Serve qualquer serviço compatível com o `/audio/transcriptions` da OpenAI — a
própria OpenAI, a Groq, um Speaches na rede local. **Neste modo os áudios saem
da máquina**, e são a voz de outras pessoas. A decisão, e o que a LGPD pede
dela, é de quem configura; por isso a ponte nunca pega a chave de
`OPENAI_API_KEY` só porque ela está no ambiente.

### Configuração

| variável | padrão | |
|---|---|---|
| `WHATSAPP_READER_TRANSCRICAO` | `local` | `local`, `api` ou `desligada` |
| `WHATSAPP_READER_IDIOMA` | `pt` | vazio deixa o motor detectar |
| `WHATSAPP_READER_WHISPER` | `whisper-cli` | o executável |
| `WHATSAPP_READER_WHISPER_MODELO` | o primeiro em `modelos/` | o arquivo `.bin` |
| `WHATSAPP_READER_WHISPER_THREADS` | metade dos núcleos | |
| `WHATSAPP_READER_FFMPEG` | `ffmpeg` | o executável |
| `WHATSAPP_READER_API_URL`, `_API_MODELO`, `_API_CHAVE` | | só no modo `api` |
| `WHATSAPP_READER_MIDIA_DIAS` | `7` | até quantos dias para trás um áudio entra na fila |
| `WHATSAPP_READER_MIDIA_GUARDAR_DIAS` | `0` | apaga o arquivo depois de N dias; `0` guarda para sempre |

As variáveis valem na janela do `serve`. O `mcp` roda com o ambiente do agente,
que é outro — por isso o motor, e o que falta nele, vão para o banco, e é de lá
que o `estado_da_ponte` os lê.

### Vocabulário

Nome de ferramenta, de gente, de rua e de condomínio é onde a transcrição mais
erra: um "viu, Claude?" falado sai "viu, Cloud?". O remédio tem duas partes —
uma **dica** que vai junto com o áudio e puxa a grafia certa, e a **correção**
dos erros já conhecidos no texto que volta. O texto como o motor entregou fica
guardado ao lado do corrigido.

A dica é pequena (o Whisper aproveita uns 600 caracteres), então ela é montada
por ordem de importância, com o mais importante no fim:

| fonte | onde | quem mantém |
|---|---|---|
| base | embutido: Claude Code, ChatGPT, Gemini, Copilot, WhatsApp… | este projeto |
| pacotes de ofício | `vocabulario.d/*.txt`, no diretório da ponte | o plugin ou skill que instala |
| pessoal | `vocabulario.txt`, no diretório da ponte | você |
| conversa | o nome do contato e o seu | ninguém: é automático |

Quando não cabe tudo, sai primeiro o base. O formato é o mesmo nos arquivos:

```
# pacote: corretor-imoveis
CRECI
ITBI
Claude Code: Cloud Code
```

Uma forma certa por linha; depois dos dois-pontos, os erros que o motor já
cometeu com ela. A correção vale só para a palavra inteira, e erro que também é
palavra de verdade não entra: "Cloud Code" sim, "Cloud" sozinho não (existe o
Google Cloud). Uma correção nova vale também para o que já foi transcrito, e
tudo é relido a cada áudio, sem reiniciar.

```sh
whatsapp-reader vocabulario                            # pacotes, dica e correções
whatsapp-reader vocabulario --conversa "João"          # a dica de uma conversa
whatsapp-reader vocabulario instalar corretor.txt      # como um plugin traz o seu pacote
whatsapp-reader vocabulario remover base               # desliga o embutido
whatsapp-reader verificar --sem-vocabulario nota.ogg   # a mesma nota sem dica, para comparar
```

Pacote de ofício não mora neste repositório: a ponte é genérica, e o vocabulário
de corretor de imóveis vem com o plugin de corretor.

Numa nota de voz real, sem a dica saiu "viu, Cloud?"; com ela, "viu, Claude".

### Quando algo falta

Faltou o ffmpeg, o modelo não carregou, a chave foi recusada: a fila **pausa sem
gastar tentativa**, e o `estado_da_ponte` diz `transcrição: PARADA` com o
motivo. Instalado o que faltava com o daemon no ar, ela volta sozinha em até
cinco minutos. Erro de um áudio só — arquivo que o ffmpeg não lê, por exemplo —
não para a fila: aquele áudio fica marcado como falho e os outros seguem.

### Retenção

Os arquivos ficam em `midia/`, com o nome do hash do conteúdo: áudio encaminhado
é baixado e transcrito uma vez só. Com `WHATSAPP_READER_MIDIA_GUARDAR_DIAS`, o
arquivo sai do disco depois de N dias, e a transcrição e a chave ficam. Um
arquivo só sai quando nenhuma mensagem que aponta para ele ainda precisa dele.

## Os dados

Ficam em `~/.kapstan/whatsapp-reader` (ou onde `WHATSAPP_READER_DIR` apontar):

```
mensagens.db      conversas, mensagens, mídias e transcrições, em WAL
sessao.db         as chaves da sessão — é isto que mantém você conectado, em WAL
midia/            os áudios baixados, pelo hash do conteúdo
modelos/          os modelos do whisper.cpp, se o motor for o local
vocabulario.txt   o vocabulário pessoal da transcrição, se existir
vocabulario.d/    os pacotes de vocabulário instalados por plugins
nao-contatar.txt  quem não pode receber mensagem, se existir
```

**Nada sai da máquina.** Não há servidor, telemetria nem sincronização. A única
exceção é escolha sua: o modo `api` da transcrição, que manda os áudios para o
serviço configurado.

**Cópia de segurança, só com o `serve` parado**, e levando junto os `-wal` e
`-shm` de cada banco: em WAL, o `.db` sozinho não tem o que chegou por último. E
nunca restaure um `sessao.db` antigo por cima de um novo — as chaves da sessão
andam a cada mensagem, e uma cópia velha não decifra o que vem depois.

Tempo é gravado como inteiro, segundos desde a época, e não como `TIMESTAMP`:
`COALESCE(em, 0)` faz a coluna perder o tipo declarado e o driver recusa
converter para `time.Time`, e o formato de texto de quem grava não é
obrigatoriamente o de quem lê. Inteiro compara igual em qualquer caminho.

O banco muda por migração numerada, e só acrescentando: tabela nova ou coluna
com padrão, nunca renomear nem remover. É o que deixa um binário antigo
continuar lendo um banco que um binário novo já migrou.

## Usar como biblioteca

Os pacotes de mídia e de transcrição não dependem do resto da ponte, e servem a
outro projeto Go:

| pacote | o que faz | importa |
|---|---|---|
| `midia` | extrai o anexo de uma mensagem, baixa sem deixar arquivo pela metade, pede ao celular o link que venceu | whatsmeow |
| `transcricao` | o contrato (`Motor`, `Audio`, `Pedido`, `Resultado`) e a classificação dos erros | só a biblioteca padrão |
| `transcricao/whispercpp` | whisper.cpp local, pela `whisper-cli` | só a biblioteca padrão |
| `transcricao/openai` | API compatível com a da OpenAI | só a biblioteca padrão |
| `transcricao/ffmpeg` | conversão para o WAV de 16 kHz | só a biblioteca padrão |
| `transcricao/vocabulario` | monta a dica por prioridade e orçamento, e corrige erros conhecidos | só a biblioteca padrão |

```go
motor := whispercpp.Novo(whispercpp.Config{
	Modelo:    "modelos/ggml-large-v3-turbo-q5_0.bin",
	Conversor: ffmpeg.Novo(ffmpeg.Config{}),
})
r, err := motor.Transcrever(ctx,
	transcricao.Audio{Caminho: "nota.ogg"},
	transcricao.Pedido{Idioma: "pt", Dica: "seu João, calhas"})
switch transcricao.Classificar(err) {
case transcricao.Ok:
	fmt.Println(r.Texto)
case transcricao.Passageiro: // tente este áudio mais tarde
case transcricao.Definitivo: // este áudio não vai dar
case transcricao.Configuracao: // falta instalar algo: pare a fila
case transcricao.Cancelado: // não conte a tentativa
}
```

Nenhum usa CGO, e `transcricao/...` não importa nada fora da biblioteca padrão —
o CI confere. Motor novo é subpacote novo de `transcricao`, sem mexer no resto.
A API é v0: pode mudar entre versões menores até a v1.

## Manutenção — leia antes de abrir issue

**A falha mais provável deste projeto tem sintoma mudo.** Quando o WhatsApp
atualizar o protocolo, a conexão passa a falhar assim:

```
websocket: close 1006 (abnormal closure): unexpected EOF
```

Isso não diz o que aconteceu, e o que aconteceu é que a versão do whatsmeow
envelheceu. O conserto:

```sh
go get -u go.mau.fi/whatsmeow@latest
go mod tidy
CGO_ENABLED=0 go build -o whatsapp-reader .
```

Se a compilação quebrar depois disso, é porque a API mudou — já aconteceu com
`Download`, `sqlstore.New`, `GetFirstDevice`, `GetGroupInfo` e `GetContact`,
que passaram a exigir `context.Context`. Costuma ser conserto mecânico.

O daemon também trata `ClientOutdatedEv`, que é o mesmo diagnóstico chegando
com nome em vez de código de erro.

## O que você precisa saber antes de usar

**Não é um programa oficial do WhatsApp.** Ele fala o mesmo protocolo do
WhatsApp Web, e a Meta não aprova esse tipo de acesso. Contas são bloqueadas
por disparo em massa — que é justamente o que este projeto recusa fazer. O
risco é baixo. Não é zero, e o número é seu.

Ele ocupa uma das quatro vagas de dispositivo conectado, e o WhatsApp pede o QR
de novo a cada ~20 dias.

E as conversas são de outras pessoas: ao guardá-las no seu disco — e, com a
transcrição, também as vozes —, o responsável por esses dados passa a ser você.

## Licença

MIT. O whatsmeow é MPL-2.0 e entra como dependência, sem modificação.

O whisper.cpp (MIT), o ffmpeg (LGPL ou GPL, conforme a compilação) e os modelos
Whisper (MIT) não vêm junto: são instalados à parte e rodam como programas
separados. Nada deles é embutido nem linkado neste binário.
