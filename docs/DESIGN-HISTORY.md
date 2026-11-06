# Fulcrum agent-s3 — promemoria di progettazione

**Stato: progettazione sospesa; nessun codice implementato o test eseguito.**

Questo repository conserva il contesto per riprendere il lavoro in un'altra sessione. Nel frattempo si prosegue con `../agent-mysql`, per cui è stato scelto Docker come runtime. Incus è stato valutato e accantonato per ora: non è una dipendenza di questo progetto.

## Obiettivo

Realizzare un agent Fulcrum open source, integrato con il core, che fornisca servizi di object storage compatibili con S3.

## Decisioni approvate

- Modello **condiviso**: un servizio Fulcrum corrisponde a un bucket e alle relative credenziali, non a un'intera istanza storage dedicata.
- **Backend RustFS predisposto dal provider**, eventualmente eseguito tramite Docker con storage persistente.
- L'agent è responsabile di **bucket, credenziali e quote**. Installazione e gestione di host, container e cluster del backend non fanno parte del perimetro iniziale.
- L'agent può essere remoto: usa le API del backend e non deve risiedere sull'host storage.
- Le credenziali di un servizio devono essere limitate al proprio bucket; non consegnare credenziali amministrative ai clienti.
- Agent MySQL e S3 restano progetti separati. Non occorre creare ora una libreria Docker comune: le normali operazioni dell'agent S3 non richiedono Docker.
- Il lavoro S3 verrà ripreso in una sessione separata.

L'accettazione del modello non costituisce ancora un contratto completo di implementazione. RustFS rimane da validare sul carico e sulla release scelti.

## Proposta tecnica da verificare

- Go con riuso di `../agent-lib-go`, seguendo la struttura di `../agent-proxmox-fae/internal/vmagent`.
- SDK S3 per operazioni standard su bucket e oggetti.
- API amministrative RustFS per utenti, policy, credenziali e quote: queste operazioni non sono automaticamente portabili ad altri server S3.
- Configurazione dell'agent con endpoint e credenziali amministrative a privilegi minimi compatibili con le operazioni richieste.
- Risultato del servizio: endpoint, nome bucket, eventuale region e informazioni per accedere alle credenziali tramite i meccanismi sicuri del core.
- Lifecycle specifico per bucket: non copiare automaticamente start/stop delle VM o dei container.

Non sono ancora scelti SDK, schema delle proprietà, azioni, stati, nomi delle metriche o modalità di consegna dei secret generati dall'agent. Verificare quest'ultima nel core prima di progettarla.

## Quanto emerso dalla ricerca su RustFS

La documentazione consultata descrive:

- Licenza Apache 2.0 per RustFS; la licenza di questo agent resta da decidere.
- Deployment tramite container Docker/Podman.
- IAM con utenti, policy, service account e credenziali temporanee.
- Un sottoinsieme testato dell'API S3, non equivalenza completa con Amazon S3.
- Quote hard per bucket espresse in byte, non quote di richieste o prestazioni.

Al momento della ricerca, l'API GitHub delle release riportava `1.0.4`, pubblicata il 25 ottobre 2026 e non marcata come prerelease. **È solo un dato storico della ricerca, non una versione fissata né una garanzia di maturità.** Ricontrollare release e note prima di implementare.

### Attenzione particolare alle quote

La documentazione delle quote segnala che:

- se la configurazione interna non può essere letta o interpretata, RustFS può permettere la scrittura ed emettere `Bucket quota check degraded to allow`;
- le statistiche di utilizzo possono riflettere le modifiche con ritardo;
- sovrascrivere un oggetto può richiedere spazio disponibile per l'intero nuovo oggetto;
- il controllo preventivo della quota è consultivo, non una prenotazione di capacità.

Non promettere quindi una quota invalicabile o una misura precisa per fatturazione senza verificare il comportamento della versione scelta, incluse scritture concorrenti e multipart.

### Durabilità e isolamento

- Un volume Docker persistente non è un backup e non offre replica o failover.
- L'isolamento tramite policy è logico: CPU, I/O, capacità e guasti del backend rimangono condivisi.
- La topologia storage, le garanzie di durabilità e il piano di recupero sono responsabilità infrastrutturali da esplicitare, non capacità automaticamente fornite dall'agent.

## Domande aperte per la prossima sessione

Affrontarle una alla volta, nell'ordine delle dipendenze; non sono requisiti approvati.

1. Quali client e funzionalità S3 sono indispensabili: operazioni base, multipart, URL presigned, versioning, lifecycle, cifratura, Object Lock?
2. Quale release e topologia RustFS useremo per sviluppo e verifica? Quali garanzie operative offre il provider?
3. Un'istanza di agent gestisce un solo backend o più endpoint/backend? Come si assegna il backend a un servizio?
4. Quali proprietà e azioni espone il servizio: creazione, modifica quota, rotazione credenziali, sospensione, cancellazione?
5. Una o più identità/chiavi per servizio? Come si generano, consegnano, custodiscono, ruotano e revocano senza esposizioni?
6. La quota è obbligatoria e con quale semantica? Come gestire esaurimento della capacità condivisa e indisponibilità dell'enforcement?
7. Cosa significa cancellare un servizio con oggetti, versioni, upload incompleti o retention attiva? Nessuna cancellazione forzata implicita.
8. Come rendere le operazioni idempotenti e recuperabili dopo timeout o completamenti parziali, senza toccare bucket non gestiti?
9. Quali metriche, segnali di salute e criteri di accettazione sono necessari?
10. Quale licenza, packaging e modalità di distribuzione adottare per l'agent?

## Verifiche raccomandate prima dell'adozione

- Due servizi con bucket e credenziali distinti: il cliente A non può leggere, scrivere o amministrare le risorse di B.
- Creazione ripetuta e ripresa dopo errore senza bucket o identità duplicati/orfani.
- Rotazione e revoca effettiva delle credenziali, comprese le richieste già firmate se nel perimetro.
- Quota su upload normale, multipart, copia, sovrascrittura e richieste concorrenti.
- Persistenza dopo riavvio e procedura di backup/restore verificata su dati di prova.
- Backend irraggiungibile e ripresa, senza perdere associazioni o riportare falsi successi.
- Cancellazione conforme alla policy approvata, inclusi bucket non vuoti e versionati.

Sono proposte per i futuri criteri di accettazione, non test già eseguiti.

## Fonti da ricontrollare

- [RustFS repository e licenza](https://github.com/rustfs/rustfs)
- [Release RustFS](https://github.com/rustfs/rustfs/releases)
- [Installazione container](https://docs.rustfs.com/en/installation/container)
- [Matrice compatibilità S3](https://docs.rustfs.com/en/reference/s3-compatibility)
- [IAM](https://docs.rustfs.com/en/security-compliance/iam)
- [Quote per bucket](https://docs.rustfs.com/en/administration/data/bucket/quota)

Le pagine online possono seguire lo sviluppo corrente e non la release installata: confrontare sempre documentazione, codice e comportamento osservato.
