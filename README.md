# Fulcrum agent-s3

Agent Fulcrum per bucket su un backend RustFS condiviso.

**Stato: implementazione fermata al controllo preliminare di compatibilità IAM.**
Non esiste ancora un agent eseguibile; questo repository contiene il contratto approvato,
il banco di prova riproducibile e le evidenze. Non è una consegna completata.

- [Contratto approvato](docs/CONTRACT.md)
- [Risultati e decisione aperta](docs/COMPATIBILITY.md)
- [Storico della progettazione](docs/DESIGN-HISTORY.md)

## Architettura approvata

Stesso impianto di `agent-proxmox-fae`: Go, `agent-lib-go/stdagent`, client del backend
dietro interfaccia, handler delle azioni, health e metriche. Nessuna estensione di core,
libreria o altri repository.

Un servizio corrisponde a un bucket privato con quota obbligatoria e credenziali dedicate.
Il cliente fornisce il secret tramite una proprietà `ephemeral`; l'agent non lo conserva.
Sono previsti rotazione, suspend/resume e **force delete distruttivo**.
I dettagli e i limiti accettati del core sono nel contratto.

Il provider predispone RustFS. L'agent non gestisce container, host, cluster o HA.
Questa prima fase riguarda solo HTTP/path-style in un ambiente locale isolato, non produzione.

## Banco di prova RustFS

Prerequisiti: Docker con Compose, Python 3 con venv, accesso ai registri delle dipendenze.
Non usare questi comandi contro dati o credenziali reali.

```sh
python3 scripts/test-env.py
python3 -m venv .local/venv
.local/venv/bin/pip install -r tests/requirements.txt
docker compose --project-name fulcrum-s3-test --env-file .local/rustfs.env up -d
```

Il backend è fissato alla release 1.0.4 e al digest OCI indicato in `compose.yaml`.
La porta è pubblicata solo su `127.0.0.1:19000`; il volume è separato e persistente.
Le credenziali casuali sono in `.local/rustfs.env`, modalità 0600, escluse da Git.
Lo script non sovrascrive un file esistente. Non usare `docker compose config`
senza `--quiet` nei log: l'output completo includerebbe le variabili risolte.

Aspettare che `curl --fail http://127.0.0.1:19000/health` riesca, quindi:

```sh
.local/venv/bin/python tests/backend_contract.py
.local/venv/bin/python tests/test_iam_boundaries.py
```

Il primo comando copre alcuni prerequisiti S3, non tutto il contratto.
Il secondo contiene i controlli negativi IAM ed **è attualmente fallente** su 1.0.4:
l'esito non va ignorato o convertito in successo. Vedere le evidenze prima di proseguire.

Le prove creano dati sintetici con nomi casuali e cancellano solo le risorse create
dalla propria esecuzione. Per fermare il container mantenendo i dati del banco:

```sh
docker compose --project-name fulcrum-s3-test --env-file .local/rustfs.env stop
```

Se si cambia porta, impostare lo stesso `RUSTFS_TEST_PORT` per Compose e per le prove.

## Sicurezza e distribuzione

Niente secret in Git, log o dati runtime non protetti. Non esporre questo banco a Internet.
HTTP non offre confidenzialità. Compatibilità S3 completa, quote invalicabili, durabilità
e failover non sono garantiti da questa verifica.

Licenza dell'agent: [Apache-2.0](LICENSE). Il repository `fulcrumproject/utils` è pubblico
ma non dichiara una licenza: chiarire il riuso prima di distribuire binari/immagini.

Repository: https://github.com/fulcrumproject/agent-s3
