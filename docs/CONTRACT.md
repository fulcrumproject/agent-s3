# Contratto approvato — agent-s3 v1

Approvato dall'utente durante il confronto di progettazione. Destinazione:
https://github.com/fulcrumproject/agent-s3 (pubblico, organizzazione fulcrumproject).

## Perimetro

- Go, stessa architettura di agent-proxmox-fae: cmd/agent, internal/config,
  internal/s3agent (handler, health, metriche), internal/rustfsapi (client dietro interfaccia).
- Riutilizzare agent-lib-go/stdagent e utility Fulcrum, senza modificare altri repository.
- AWS SDK for Go v2 per S3 e firma SigV4; client amministrativo RustFS interno.
- Un backend condiviso per agent, un bucket privato per servizio. Una replica attiva per agentId.
- Backend predisposto dal provider. L'agent non gestisce Docker, host o cluster.
- RustFS 1.0.4 da validare; immagine di test fissata per digest.
- HTTP, path-style, localhost/rete di prova isolata. Endpoint backend ed endpoint cliente distinti.
  Nessuna esposizione Internet né validazione per produzione in questa fase.

## Criteri di accettazione

1. Create: nome bucket e accessKeyId derivati dal serviceId, immutabili; nome visualizzato indipendente.
   Nessuna adozione implicita di risorse preesistenti, né prove di ownership basate solo sul nome.
2. Secret fornito dal cliente tramite proprietà ephemeral. Mai restituito in agentInstanceData,
   salvato su disco dall'agent o registrato nei log. Il cliente conserva la propria copia.
   Quando il vault lo ha eliminato, il ritentativo richiede reinvio esplicito.
3. Credenziali limitate alle operazioni S3 approvate sul proprio bucket: lettura, scrittura,
   elenco, cancellazione degli oggetti, multipart, URL prefirmati PUT/GET.
   Vietati altri bucket, accesso anonimo, gestione IAM/policy/ACL/configurazione bucket,
   creazione di altre chiavi e creazione/eliminazione diretta dei bucket.
4. Quota positiva obbligatoria in byte, applicata e riletta prima di abilitare il cliente.
   Nessun fallback illimitato. Update: aumento o no-op, riduzione rifiutata senza ridurre il limite backend.
5. Rotazione tramite update: accessKeyId invariato, nuovo secret cliente, una sola coppia attiva,
   nessuna sovrapposizione. Verificare rifiuto della vecchia firma e dei vecchi URL prefirmati.
6. Suspend/resume: disabilitazione/riabilitazione reversibile delle credenziali, senza reinvio secret.
   Dati e quota invariati. Nessuna promessa di interrompere richieste già in corso;
   URL non scaduti possono tornare validi al resume.
7. Delete significa force delete: oggetti, eventuali versioni/delete marker, multipart incompleti,
   bucket, credenziali e policy del solo servizio. Non bypassare retention/Object Lock.
   Disabilitare l'accesso prima di cancellare dati. Fallimenti parziali lasciano accesso bloccato,
   senza riattivazione automatica; un nuovo delete riprende la pulizia. Non dichiarare falsi successi.
8. Handler idempotenti e recuperabili su risorse di ownership verificata. Non ripetere distruttivamente
   un'operazione su risorse estranee o di ownership incerta. Nessun framework di recovery nuovo.
9. OnHealth: backend e autenticazione. OnMetrics: uso riportato dal backend, quota, rapporto uso/quota.
   Non inventare zeri in caso di errore; statistiche potenzialmente ritardate, non valide per fatturazione.
10. Logger locale sicuro: stdagent registra la configurazione dopo la risoluzione dei secret;
    impedirne l'emissione senza modificare la libreria e coprire il caso con un test.
11. Test unitari e test reali su RustFS 1.0.4 e core invariato obbligatori. Client AWS CLI e boto3.
    Coprire isolamento di due servizi, API vietate, multipart/presigned, quote (anche concorrenza,
    copia e sovrascrittura), rotazione, suspend/resume, force delete e fallimenti parziali.
    Documentare esiti reali: le incompatibilità bloccano il completamento, senza aggiramenti o
    modifiche degli altri repository.
12. Consegna: binario Go, Dockerfile non-root, Compose di prova, esempi AgentType/ServiceType,
    documentazione e licenza Apache-2.0. Versioni delle dipendenze fissate.

## Limiti accettati del core e del runtime

stdagent esegue job Pending, non riprende automaticamente Processing dopo un crash.
Il worker di manutenzione del core, se abilitato, può marcare i job scaduti Failed;
successivamente si può richiedere un nuovo tentativo. Il timeout corrente usa createdAt,
non claimedAt. La sua procedura non applica le transizioni lifecycle né la pulizia dei
secret: ephemeral non garantisce una scadenza temporale dopo un crash.
La configurazione dei timeout del banco di prova deve essere esplicita.

Nessun secret persistente nascosto per aggirare questi limiti. Eventuali blocchi del backend
o del core vanno riportati, non risolti cambiando gli altri repository.

## Esclusioni

Versioning/lifecycle/Object Lock/cifratura gestiti dal cliente, TLS e relativa infrastruttura,
HA dell'agent/backend, gestione di host/container/cluster, import di bucket, nomi personalizzati,
quote riducibili o illimitate, multiple credenziali simultanee, metriche di traffico/richieste,
garanzie di durabilità, fatturazione o compatibilità S3 completa, Helm e pacchetti DEB/RPM.

## Pubblicazione

Repository pubblico creato. L'utente ha autorizzato commit locali su main e push.
La distribuzione resta subordinata alle verifiche e alla revisione dei contenuti.
Prima di distribuire binari/immagini chiarire la licenza di fulcrumproject/utils:
il repository è pubblico ma non dichiara una licenza. Non modificarlo.
