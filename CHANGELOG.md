# Mudanças

O formato segue o [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/), e
as versões, o [SemVer](https://semver.org/lang/pt-BR/). Até a v1, uma versão
menor pode mudar a API dos pacotes.

## [0.2.0] — 2026-09-16

### Novo

- Os áudios de conversa individual são baixados e transcritos pelo daemon, sem
  atrasar o recebimento: whisper.cpp local por padrão, ou uma API compatível com
  a da OpenAI, por escolha explícita.
- `listar_mensagens` mostra a transcrição (`[áudio 0:42 · transcrição] …`) e
  busca dentro dela; áudio sem transcrição diz por quê.
- `ultima_interacao` traz o que foi dito quando a última mensagem é um áudio.
- `estado_da_ponte` mostra a fila dos áudios, o motor de transcrição e o que o
  impede de andar.
- Subcomando `verificar [arquivo]`: o que falta instalar, e quanto a máquina
  demora para transcrever.
- Vocabulário em camadas para a transcrição: um pacote base embutido (agentes
  de IA e WhatsApp), pacotes de ofício instalados por plugin em
  `vocabulario.d/`, o `vocabulario.txt` pessoal e o nome do contato. A dica
  respeita um orçamento, os erros conhecidos são corrigidos, e uma correção
  nova vale para as transcrições antigas. Subcomando `vocabulario` e
  `verificar --sem-vocabulario`.
- Retenção opcional dos arquivos de áudio (`WHATSAPP_READER_MIDIA_GUARDAR_DIAS`),
  sem perder transcrição nem chave.
- Pacotes importáveis: `midia`, `transcricao`, `transcricao/whispercpp`,
  `transcricao/openai`, `transcricao/ffmpeg`, `transcricao/vocabulario`.
- CI nos três sistemas, com detector de corrida, staticcheck, govulncheck e as
  regras de dependência entre os pacotes.
- Binários prontos a cada versão marcada, pelo GoReleaser.
- **Uma ponte por máquina:** o `serve` recusa subir enquanto outro daemon
  estiver batendo, e nomeia o processo. Dois clientes sobre o mesmo `sessao.db`
  corrompem o ratchet do Signal — o pid já ia na batida, faltava compará-lo.

### Corrigido

- O `sessao.db` abria sem `busy_timeout` nem WAL: numa rajada de mensagens
  atrasadas, parte delas saía sem decifrar (`SQLITE_BUSY`).
- As mensagens de conversa temporária vindas do histórico eram descartadas como
  vazias — texto e áudio.
- `serve` e `mcp` subindo juntos num banco novo podiam falhar com `SQLITE_BUSY`
  ao ligar o WAL.

### Mudou

- O banco ganhou migrações numeradas (`PRAGMA user_version`), só aditivas: um
  binário antigo continua lendo um banco migrado. A segunda guarda, em cada
  transcrição, o texto bruto do motor e a dica que foi junto.

## [0.1.0]

- Primeira versão marcada: leitura das conversas por MCP, envio de uma mensagem
  por vez com prévia obrigatória, e o diagnóstico da ponte pela batida no banco.
