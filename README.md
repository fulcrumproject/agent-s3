# Fulcrum agent-s3

Agent Go per servizi di object storage su **RustFS 1.0.4**, integrato con Fulcrum Core.

**Prima versione per sviluppo/collaudo isolato, non validata per produzione.**
Implementa creazione, quote, rotazione, sospensione e force delete. Le verifiche locali
comprendono il core invariato, AWS CLI, boto3, multipart, quote concorrenti e recupero
da cancellazioni parziali. Vedere [evidenze e limiti](docs/VERIFICATION.md).

## Modello e confine di sicurezza

- Un servizio = un bucket privato con credenziali dedicate; un backend per agent.
- Bucket e accessKeyId derivati dal serviceId, non dal nome visualizzato.
- Secret fornito dal cliente, proprietà `ephemeral` nel core; nessuna copia locale nell'agent.
- Quota positiva obbligatoria in byte, aumentabile ma non riducibile.
- Una sola coppia cliente attiva; rotazione senza sovrapposizione.
- `suspend` / `resume` preservano dati e credenziali.
- **`delete` è distruttivo:** svuota il bucket, rimuove versioni/delete marker, interrompe
  multipart incompleti e cancella bucket, utente e policy. Non bypassa retention o legal hold.

**I clienti devono raggiungere soltanto il gateway S3, non RustFS direttamente.**
La separazione è necessaria: le API self-service di RustFS non rispettano tutti i
dinieghi amministrativi nel modo richiesto dal servizio. Il provider deve rendere
privato l'endpoint amministrativo. Un URL diverso, senza isolamento di rete, non basta.

Il gateway HTTP è un componente infrastrutturale separato: **l'agent non trasporta
gli oggetti e non gestisce Docker/host/cluster**. Il banco usa solo HTTP/path-style;
non esporlo a Internet. [Dettagli IAM e gateway](docs/COMPATIBILITY.md).

## Architettura

Stesso impianto di `agent-proxmox-fae`, senza estensioni agli altri repository:

- `cmd/agent`: avvio, configurazione locale, logger protetto e shutdown;
- `internal/s3agent`: handler, ownership, health e metriche tramite `stdagent`;
- `internal/rustfsapi`: AWS SDK Go v2 e client amministrativo RustFS firmato SigV4;
- `internal/config`: configurazione locale e remota;
- `internal/safelog`: blocca i dump della configurazione risolta della libreria comune.

Una replica attiva per `agentId`. Nessun database locale, orchestratore alternativo
o framework multi-backend.

## Build

Usato nelle verifiche: Go 1.27.0, Linux amd64.

```sh
go test ./...
go test -race ./...
go vet ./...
go build -trimpath -o bin/agent-s3 ./cmd/agent
docker build -t fulcrum-agent-s3:dev .
```

L'immagine è non-root (`10001:10001`), senza shell. Builder e immagini del banco
sono fissati per digest. Binari/immagini non vengono pubblicati automaticamente.

## Configurazione Fulcrum

1. Registrare [ServiceType](examples/service-type.json).
2. Inserire il suo ID in `serviceTypeIds` dell'[AgentType](examples/agent-type.json), poi registrarlo.
3. Registrare i [tre tipi di metrica](examples/metric-types.json).
4. Creare l'agent associato al provider e all'AgentType, con `configuration.rustfs`:
   `endpoint` privato, `clientEndpoint` S3-only, `region`, `accessKey`, `secretKey`.
   Le due credenziali amministrative sono proprietà secret persistenti nel vault.
5. Creare un token di ruolo `agent` con `scopeId` uguale all'ID dell'agent.
6. Configurare localmente solo URL del core e token, quindi avviare il binario.

[.env.example](.env.example) mostra le variabili. Non salvare token o secret in Git;
se si usa un file locale, limitarne i permessi a 0600. Non abilitare dump HTTP.

```sh
export S3_AGENT_FULCRUM_API_URL=http://127.0.0.1:18000
# Fornire S3_AGENT_FULCRUM_API_TOKEN tramite il proprio meccanismo sicuro.
./bin/agent-s3
```

La configurazione backend viene caricata alla connessione iniziale; modificare credenziali
o timeout richiede il riavvio. Endpoint e region sono immutabili nello schema: non spostare
un agent esistente su un altro backend. Gli esempi non aggiungono template di installazione remota.

### Proprietà e azioni

Creazione: `POST /api/v1/services`, con `agentId`, `serviceTypeId`, `groupId`, `name`
e `properties`:

```json
{
  "quotaBytes": 1073741824,
  "secretAccessKey": "<secret casuale forte fornito dal cliente>"
}
```

Il client conserva il secret originario. Il risultato in `agentInstanceData` contiene
soltanto `endpoint`, `bucket`, `accessKeyId`, `region` e `quotaBytes`.
Usare endpoint/region restituiti con SigV4 e addressing style `path`.
Gli URL prefirmati sono generati dal client S3, non da un'API aggiuntiva dell'agent.

| Operazione | API core |
| --- | --- |
| Aumento quota / nuovo secret | `PATCH /api/v1/services/{id}`, campo `properties` |
| Sospensione | `POST /api/v1/services/{id}/suspend` |
| Riattivazione | `POST /api/v1/services/{id}/resume` |
| Force delete | `DELETE /api/v1/services/{id}` |

Le operazioni sono asincrone. Verificare il job e lo stato del servizio, non soltanto
l'accettazione della richiesta HTTP. [Recupero e limiti del core](docs/OPERATIONS.md).

## Banco di prova completo

Prerequisiti: Docker locale con Compose, Python 3 con venv e un checkout **pulito**
del core alla revisione in [tests/core-revision](tests/core-revision).
Il core è pubblico: può essere clonato in una directory separata.
Lo script legge e compila quel checkout; non lo modifica.

```sh
python3 -m venv .local/venv
.local/venv/bin/pip -q install -r tests/requirements.txt
CORE_DIR=../core bash scripts/prepare-e2e.sh
.local/venv/bin/python tests/backend_contract.py
.local/venv/bin/python tests/test_iam_boundaries.py
bash tests/network_boundary.sh
go build -race -o .local/agent ./cmd/agent
.local/venv/bin/python -u tests/e2e.py
```

L'ultima prova usa il vero agent, riavvia l'agent e **ferma/riavvia soltanto il backend
del banco**, crea oltre 1000 oggetti e verifica un legal hold sintetico.
Non eseguire due suite E2E contemporaneamente sullo stesso banco.

Le prove controllano identità dei container, progetto Compose e binding loopback
prima di scrivere tramite API. Usano dati sintetici, credenziali casuali e solamente
le risorse create dalla propria esecuzione. In caso di errore possono rimanere risorse
di prova: vengono indicati gli ID, senza pulizie indiscriminate.

| Endpoint locale | Scopo |
| --- | --- |
| `127.0.0.1:19080` | gateway cliente S3 |
| `127.0.0.1:19000` | RustFS privato per agent/operatori fidati |
| `127.0.0.1:18000` | core di prova |

Il core di prova conserva il bootstrap preesistente, valido 24 ore: **non è una
configurazione di produzione**. Credenziali DB/vault/backend sono generate in
`.local/*.env` con permessi 0600; i volumi sono separati e persistenti.
Non stampare `docker compose config` senza `--quiet`: mostrerebbe valori risolti.

Per fermare il banco senza eliminare i volumi:

```sh
docker compose --project-name fulcrum-s3-test \
  --env-file .local/rustfs.env --env-file .local/core.env \
  -f compose.yaml -f compose.e2e.yaml stop
```

La CI esegue controlli Go, build e suite reale su fixture proprie, senza pubblicare immagini.

## Limiti e licenza

Nessuna garanzia di compatibilità S3 completa, quota fisicamente invalicabile, fatturazione,
durabilità o HA. Statistiche potenzialmente ritardate; richieste già in corso non vengono
necessariamente annullate da sospensione/rotazione. Versioning, Object Lock e lifecycle
non sono configurabili dal cliente in questa versione.

Licenza del progetto: [Apache-2.0](LICENSE). `fulcrumproject/utils` è pubblico ma non
dichiara una licenza: chiarire il riuso prima di distribuire binari/immagini.

[Contratto](docs/CONTRACT.md) · [Operazioni](docs/OPERATIONS.md) ·
[Verifiche](docs/VERIFICATION.md) · [Storico](docs/DESIGN-HISTORY.md)
