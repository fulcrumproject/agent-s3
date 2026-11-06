# Verifica iniziale RustFS 1.0.4

**Esito: prerequisiti S3 parzialmente verificati; requisito di amministrazione
esclusiva dell'agent non soddisfatto dal modello IAM provato. Agent non implementato.**

## Ambiente e riproducibilità

- Container ufficiale RustFS 1.0.4, indice OCI
  `sha256:ed9210d6411d77ffceb70d94b964a3cb7d225983368bc25bebe01e995293dcc2`.
- Piattaforma linux/amd64, manifest
  `sha256:e287e99762a4a3e7b706526e01f2ff755f67db165e9be9f6b907adc6fb6d6ee1`.
- Docker 29.9.3, Compose v5.7.0; endpoint HTTP su loopback, volume di prova dedicato.
- boto3 1.43.125 / botocore 1.43.125. Le altre dipendenze di test sono fissate
  in `tests/requirements.txt`.
- Dati sintetici, nomi casuali con prefisso `fulcrum-probe-` o `fc-boundary-`.
  Credenziali generate localmente e mai mostrate o versionate.
- Ogni prova pulisce solo le risorse che ha creato. Non usa risorse di produzione.

## Prove iniziali superate

Comando: `.local/venv/bin/python tests/backend_contract.py`.

- Creazione bucket e round-trip dei tag necessari per l'ownership.
- Impostazione e rilettura quota obbligatoria.
- Attesa della disponibilità delle statistiche prima di usare un bucket nuovo:
  subito dopo la creazione il PUT può restituire 503; dopo un breve intervallo ha avuto successo.
- Utente IAM limitato al bucket: PUT/GET, diniego su altro bucket, diniego modifica tag/policy.
  `ListBuckets` restituisce solo il proprio bucket, non l'altro bucket di prova.
- Diniego di `add-user` e di creazione di service account aggiuntive con policy esplicita `Deny admin:*`.
- `AssumeRole` STS rifiutato con `AccessDenied`: nessuna credenziale temporanea aggiuntiva.
- URL prefirmati GET e PUT; upload multipart a una parte.
- PUT oltre quota rifiutato senza creare l'oggetto.
- Rotazione amministrativa preserva policy; vecchio secret e URL prefirmato rifiutati.
- Suspend/resume senza reinvio del secret.

Questa non è ancora la verifica completa del contratto. Non sono ancora verificati:
agent, core end-to-end, AWS CLI, concorrenza, multipart grande e quota, copie/sovrascritture,
force delete/versioni, crash e assenza di secret nei log dell'agent.

## Differenze API riscontrate

- Admin e STS richiedono statement separati, senza `Resource: "*"`.
  Per le risorse S3 usare ARN. La policy non è stata indebolita: restano i Deny.
- Il reset amministrativo utente `set-user-secret-key` usa `secret_key`
  (snake_case), a differenza di `add-user` che usa `secretKey`.
- Il superamento quota è `InvalidRequest`, con messaggio
  `Bucket quota exceeded. ...`. La prova controlla entrambi e l'assenza dell'oggetto.

## Blocchi riprodotti: confini IAM

Comando: `.local/venv/bin/python tests/test_iam_boundaries.py`.
Le asserzioni esprimono il contratto; non sono marcate xfail né saltate.

### Utente IAM e cambio autonomo del secret

Con un utente dedicato, policy limitata al bucket e Deny espliciti per `admin:*` e `sts:*`:

1. Firmare con la credenziale cliente `POST /rustfs/admin/v3/account/password`.
2. Inviare secret attuale e nuovo usando i campi previsti dalla release.
3. Atteso dal contratto data-only: operazione negata.
4. Osservato: HTTP 200; il nuovo secret autentica correttamente.

È una funzione self-service distinta dal controllo delle azioni amministrative,
non una concessione di accesso ai dati degli altri bucket. Tuttavia contraddice
l'amministrazione IAM esclusiva dell'agent descritta dal contratto.

### Service account con parent amministrativo condiviso

L'alternativa con una service account per bucket, entrambe figlie dello stesso
utente amministrativo, non va usata senza ulteriori vincoli:

1. Creare A e B con policy distinte e Deny esplicito `admin:*`.
2. Con A richiedere la rimozione della credenziale B.
3. Atteso: B resta presente, richiesta cliente negata.
4. Osservato: HTTP 200; la successiva lettura amministrativa di B restituisce 404.

Il codice della release consente il fallback di ownership al parent. Questa
prova riguarda il parent condiviso: non afferma che tutti i modelli IAM abbiano
lo stesso comportamento. Non abbiamo adottato questa alternativa nell'agent.

## Decisione necessaria

Prima dell'implementazione occorre scegliere esplicitamente fra:

- mantenere utenti IAM dedicati, ammettendo il cambio self-service del solo proprio
  secret (fermi isolamento, quota, divieto di altre identità e controllo amministrativo);
- mantenere il contratto data-only rigoroso introducendo un confine di rete/API tra
  endpoint cliente S3 e API amministrative, da verificare e predisporre dal provider;
- rivalutare backend/release.

Nessuna eccezione adottata, nessun proxy aggiunto, nessuna modifica a RustFS, core o libreria.
Nessun completamento del contratto o idoneità alla produzione dichiarati.

## Controlli del banco di prova

- `python3 -m py_compile scripts/test-env.py tests/backend_contract.py tests/test_iam_boundaries.py`: superato.
- `.local/venv/bin/pip check`: superato.
- `docker compose --project-name fulcrum-s3-test --env-file .local/rustfs.env config --quiet`: superato.
- `go mod verify`: superato (verifica dei moduli, non compilazione/test di un agent).
- `.local/venv/bin/python tests/backend_contract.py`: superato dopo aver adattato il client al formato della release.
- `.local/venv/bin/python tests/test_iam_boundaries.py`: due fallimenti riprodotti, riportati sopra.
- Test Go, core end-to-end, build binario/immagine agent e suite completa: non eseguiti, implementazione sospesa.
