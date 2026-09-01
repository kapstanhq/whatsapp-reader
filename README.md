# whatsapp-reader

**Lê as suas conversas do WhatsApp e as entrega a um agente por MCP. Manda uma
mensagem por vez, e só depois de você ver o texto e para quem vai.**

> *WhatsApp bridge exposing MCP tools. Reading is unrestricted; sending is one
> message at a time, gated on a preview the operator must be shown first. Bulk
> messaging is not a missing feature — it is refused by design and cannot be
> expressed by the API. Written for a Portuguese-language plugin; docs below are
> in Portuguese.*

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

Não é fork de nada. São ~720 linhas que usam o
[whatsmeow](https://github.com/tulir/whatsmeow), a biblioteca que fala o
protocolo do WhatsApp:

```
banco.go     esquema SQLite e as consultas
servir.go    o daemon: conecta, pareia, captura histórico e mensagens
mcp.go       o protocolo MCP, JSON-RPC sobre stdio, escrito à mão
ponte.go     o elo entre os dois processos, e as travas do envio
envios.go    a tabela de envios: o que saiu, o que foi recusado, e por quê
cuidados.go  a lista de não contatar, a prévia que envelhece, a digitação
main.go      os dois subcomandos
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

Precisa de Go. **Não precisa de compilador C**: o driver SQLite é
`modernc.org/sqlite`, em Go puro.

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
whatsapp-reader serve     # janela própria: mantém a conexão e grava
whatsapp-reader mcp       # o que o agente executa: só lê o banco
```

**São dois processos, e a razão importa.** O `serve` precisa sobreviver ao
agente fechar: é ele que recebe as mensagens. Se a janela dele fechar, o
histórico congela no último momento em que ele esteve vivo, e **o que passou
enquanto ele esteve fora não volta**.

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
| `listar_mensagens` | mensagens por conversa, por período ou por texto |
| `ultima_interacao` | quando foi a última mensagem, de quem, e há quantos dias |
| `estado_da_ponte` | quanto está guardado e até quando |
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

## Os dados

Ficam em `~/.kapstan/whatsapp-reader` (ou onde `WHATSAPP_READER_DIR` apontar):

```
mensagens.db   conversas e mensagens, em WAL
sessao.db      as chaves da sessão — é isto que mantém você conectado
```

**Nada sai da máquina.** Não há servidor, telemetria nem sincronização.

Tempo é gravado como inteiro, segundos desde a época, e não como `TIMESTAMP`:
`COALESCE(em, 0)` faz a coluna perder o tipo declarado e o driver recusa
converter para `time.Time`, e o formato de texto de quem grava não é
obrigatoriamente o de quem lê. Inteiro compara igual em qualquer caminho.

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

E as conversas são de outras pessoas: ao guardá-las no seu disco, o
responsável por esses dados passa a ser você.

## Licença

MIT. O whatsmeow é MPL-2.0 e entra como dependência, sem modificação.
