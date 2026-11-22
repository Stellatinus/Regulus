import type { updates } from "./updates.en";

export const updatesIt: Record<keyof typeof updates, string> = {
  "updates.title": "Aggiornamenti",
  "updates.loading": "Caricamento…",
  "updates.unknown": "sconosciuta",

  "updates.channel.stable": "stabile",
  "updates.channel.beta": "beta",
  "updates.channel.alpha": "alpha",

  "updates.version.title": "Versione",
  "updates.version.runs": "Questo hub esegue",
  "updates.version.platform": "({platform}).",
  "updates.version.devBuild":
    "Questa è una build di sviluppo. Non si confronta con le release, quindi non le viene mai offerto un aggiornamento.",
  "updates.version.checksOffBefore":
    "I controlli degli aggiornamenti sono disattivati nella configurazione (",
  "updates.version.checksOffAfter":
    "), quindi l'hub non contatta nessun server.",
  "updates.version.availableLabel": "Aggiornamento disponibile",
  "updates.version.availableVersion": "Versione {version}",
  "updates.version.isAvailable": "è disponibile.",
  "updates.version.releasePage": "Pagina della release",
  "updates.version.upToDate":
    "Regulus è aggiornato: la release {channel} più recente è la {version}.",
  "updates.version.noRelease":
    "Il server delle release non elenca nessuna release {channel}.",
  "updates.version.checked": "Controllato {ago}.",
  "updates.version.notChecked": "Non ancora controllato.",
  "updates.version.checkFailed": "L'ultimo controllo non è riuscito: {error}",
  "updates.version.stale":
    "Questo hub non riesce a cercare nuove release dal {when}. Potrebbe non sapere di un aggiornamento, anche di sicurezza. Controlla che possa raggiungere api.github.com.",
  "updates.version.staleNever":
    "Questo hub non è mai riuscito a cercare nuove release. Potrebbe non sapere di un aggiornamento, anche di sicurezza. Controlla che possa raggiungere api.github.com.",
  // #263: l'indice firmato delle release (latest.json).
  "updates.index.expired":
    "L'elenco firmato delle release che questo hub ha verificato per ultimo è scaduto il {detail}. Il server delle release potrebbe nascondere una release più recente, anche una correzione di sicurezza. Riprova più tardi e guarda la pagina delle release del progetto.",
  "updates.index.replayed":
    "Il server delle release ha offerto un elenco firmato delle release del {detail}, più vecchio di uno che questo hub aveva già verificato. Qualcuno potrebbe ripetere risposte vecchie: l'hub potrebbe non sapere di una release più recente.",
  "updates.index.hidden":
    "L'elenco firmato delle release indica la versione {detail}, ma il server delle release non la elenca. Un mirror o un proxy potrebbe nasconderla: l'hub potrebbe non sapere di una release più recente.",
  "updates.index.missing":
    "Il server delle release non offre più l'elenco firmato che questo hub aveva verificato. Potrebbe nascondere una release più recente, anche una correzione di sicurezza.",
  "updates.index.invalid":
    "Non è stato possibile verificare l'elenco firmato delle release: {detail}. L'hub potrebbe non sapere di una release più recente.",

  "updates.version.lastSuccess": "Ultimo controllo riuscito: {ago}.",

  "updates.staged.label": "Aggiornamento scaricato",
  "updates.staged.version": "La versione",
  "updates.staged.downloaded": "è stata scaricata ({size}, {archive}).",
  "updates.staged.signature":
    "La sua firma è stata verificata: è stata creata dal workflow di release di questo progetto per questa versione e il file corrisponde.",
  "updates.staged.checksum":
    "Il suo SHA-256 corrisponde all'elenco dei checksum della release.",
  "updates.staged.itIs": "Risulta",
  "updates.staged.notInstalled": "non installata",
  "updates.staged.applyOn": ".",
  "updates.staged.applyOff":
    ": questo hub non è configurato per installare da solo gli aggiornamenti (updates.apply e l'unità regulus-update.path dell'installer).",
  "updates.staged.checksumNote":
    "L'elenco dei checksum arriva dallo stesso posto del file: individua un download danneggiato ma non dimostra chi lo ha pubblicato.",

  "updates.downloading": "Download in corso…",
  "updates.applying.head": "Installazione in corso: {what}…",
  "updates.applying.theUpdate": "aggiornamento",
  "updates.applying.rest":
    "L'hub si riavvia tra un momento e questa pagina può smettere di rispondere per un po'; ricontrolla da sola. Se la nuova versione non si avvia correttamente, viene rimessa la precedente.",
  "updates.stalled":
    "Nessuno ha preso in carico la richiesta da alcuni minuti. L'unità che installa gli aggiornamenti probabilmente non è in esecuzione:",
  "updates.noStatusSocket":
    "Questo hub non ha il socket di stato locale, quindi `regulus status` non mostra dati in tempo reale, e un aggiornamento da una release che non scrive ancora running.json (precedente alla correzione di #312) viene annullato su di esso. Gli aggiornamenti da questa versione in poi funzionano. Motivo:",
  "updates.trustFallback":
    "L'hub non è riuscito a scaricare la radice di fiducia Sigstore aggiornata, quindi controlla le release con quella inclusa in questa versione, che non conosce le chiavi di firma revocate da Sigstore dopo la sua creazione. Riprova da solo. Motivo:",
  "updates.last.applied": "Aggiornato alla {version} {ago}.",
  "updates.last.appliedFrom": "Aggiornato alla {version} dalla {from} {ago}.",
  "updates.last.rolledBack":
    "La versione {version} non si è avviata correttamente, quindi è stata rimessa la versione precedente.",
  "updates.last.notInstalled": "La versione {version} non è stata installata.",
  "updates.downloadFailed": "Il download non è riuscito: {error}",

  "updates.action.checkNow": "Controlla ora",
  "updates.action.checking": "Controllo in corso…",
  "updates.action.download": "Scarica {version}",
  "updates.action.install": "Installa {version}",
  "updates.action.installNow": "Installa ora",
  "updates.action.cancel": "Annulla",
  "updates.action.save": "Salva",
  "updates.action.stop": "Ferma",
  "updates.confirm.label": "Conferma installazione",
  "updates.confirm.text":
    "L'hub viene verificato e sostituito dalla versione {version} e si riavvia. Gli agent si riconnettono da soli. Se la nuova versione non si avvia, viene rimessa la precedente.",

  "updates.settings.title": "Impostazioni",
  "updates.settings.when": "Quando esce una nuova versione",
  "updates.mode.manual": "Avvisami soltanto",
  "updates.mode.download": "Scaricala e verificala, poi aspetta me",
  "updates.mode.automatic":
    "Scaricala e installala da sola, dentro una finestra di manutenzione",
  "updates.settings.applyOff":
    "L'installazione non è configurata su questo hub (updates.apply e l'unità regulus-update.path, che l'installer abilita).",
  "updates.settings.windowStarts": "Inizio della finestra",
  "updates.settings.windowEnds": "Fine della finestra",
  "updates.settings.windowNote":
    "Gli orari sono quelli del server dell'hub. L'hub si riavvia durante l'aggiornamento e una versione di cui è stato fatto il rollback non viene riprovata da sola.",
  "updates.settings.channel": "Canale",
  "updates.settings.channelStable": "Stabile",
  "updates.settings.channelBeta": "Beta (release e beta)",
  "updates.settings.channelAlpha": "Alpha (ogni release, alpha comprese)",

  "updates.rollout.title": "Server",
  "updates.rollout.intro":
    "I server gestiti da questo hub vengono aggiornati alla versione che esegue l'hub (",
  "updates.rollout.introAfter":
    "). La scaricano dall'hub, quindi non hanno bisogno di accesso a internet, e un server la cui nuova versione non si riconnette torna da solo alla precedente.",
  "updates.rollout.running":
    "Aggiornamento dei server, {size} alla volta. Si ferma al primo errore.",
  "updates.rollout.progress": "Avanzamento dell'aggiornamento",
  "updates.rollout.allCurrent": "Tutti i server sono aggiornati ({target}).",
  "updates.rollout.exceptions":
    "Tranne questi, che non si possono aggiornare da qui:",
  "updates.rollout.exceptionsLabel":
    "Server che non si possono aggiornare da qui",
  "updates.rollout.behindOne": "1 server esegue una versione più vecchia.",
  "updates.rollout.behindMany":
    "{count} server eseguono una versione più vecchia.",
  "updates.rollout.behindLabel": "Server indietro",
  "updates.rollout.runs": "{name} esegue la {version}",
  "updates.rollout.atATime": "Alla volta",
  "updates.rollout.all": "Tutti",
  "updates.rollout.updateOne": "Aggiorna questo server alla {target}",
  "updates.rollout.updateMany": "Aggiorna {count} server alla {target}",
  "updates.rollout.lastDone": "Ultimo aggiornamento dei server: completato.",
  "updates.rollout.lastDoneAgo":
    "Ultimo aggiornamento dei server: completato, {ago}.",
  "updates.rollout.lastStopped": "Ultimo aggiornamento dei server: fermato.",
  "updates.rollout.lastStoppedAgo":
    "Ultimo aggiornamento dei server: fermato, {ago}.",
  "updates.rollout.lastFailed":
    "Ultimo aggiornamento dei server: fermato da un errore.",
  "updates.rollout.lastFailedAgo":
    "Ultimo aggiornamento dei server: fermato da un errore, {ago}.",
  "updates.rollout.lastLabel": "Ultimo aggiornamento",

  "updates.agent.pending": "in attesa",
  "updates.agent.offered": "sta scaricando l'aggiornamento",
  "updates.agent.installing": "in installazione, in attesa che torni",
  "updates.agent.updated": "aggiornato",
  "updates.agent.failed": "non riuscito",
  "updates.agent.skipped": "non provato",
  "updates.agent.unknown": "esito sconosciuto",
  "updates.agent.row": "{name} ({from}): {status}",
  "updates.agent.rowMessage": "{name} ({from}): {status} — {message}",

  // #368: what the installer on a server wrote (internal/update/applycode.go).
  "updates.applyMsg.request_unreadable":
    "La richiesta di aggiornamento non si poteva leggere (non era una richiesta, era troppo grande o non era un file normale). È stata rimossa.",
  "updates.applyMsg.invalid_version":
    "La richiesta indica una versione non valida: {error}",
  "updates.applyMsg.not_newer":
    "La versione {version} non è più recente di quella installata ({installed}). Gli aggiornamenti non tornano mai indietro.",
  "updates.applyMsg.rolled_back":
    "{error}. È stata rimessa la versione {version}.{note}",
  "updates.applyMsg.rollback_failed":
    "{error}. Rimettere la {version} non è riuscito: {rollback_error}",
  "updates.applyMsg.interrupted_stands":
    "L'installazione è stata interrotta. La nuova versione è stata controllata dopo e resta installata.",
  "updates.applyMsg.interrupted_not_installed":
    "L'installazione è stata interrotta prima che la nuova versione fosse installata. Resta la {version}.",
  "updates.applyMsg.interrupted_rolled_back":
    "L'installazione è stata interrotta. È stata rimessa la versione {version}.{note}",
  "updates.applyMsg.interrupted_rollback_fail":
    "L'installazione è stata interrotta e rimettere la {version} non è riuscito: {error}",

  // #372: i passaggi che falliscono con il testo di un errore di sistema ({error} è quel testo, in inglese).
  "updates.applyMsg.version_unreadable":
    "Non è stato possibile leggere la versione installata: {error}",
  "updates.applyMsg.work_dir_failed":
    "L'installazione non ha potuto creare la sua cartella di lavoro: {error}",
  "updates.applyMsg.fetch_failed":
    "La release scaricata non è utilizzabile: {error}",
  "updates.applyMsg.extract_failed":
    "Non è stato possibile estrarre il programma dall'archivio: {error}",
  "updates.applyMsg.new_binary_mismatch":
    "Il nuovo programma non indica la versione {version} (ha indicato: {got}).",
  "updates.applyMsg.backup_failed":
    "Non è stato possibile conservare il programma attuale per un ripristino: {error}",
  "updates.applyMsg.database_copy_failed":
    "Non è stato possibile conservare una copia del database per un ripristino: {error}. Non è stato cambiato nulla.",
  "updates.applyMsg.pending_write_failed":
    "Non è stato possibile registrare l'aggiornamento in corso: {error}",
  "updates.applyMsg.swap_failed":
    "Non è stato possibile mettere a posto il nuovo programma: {error}",
  "updates.applyMsg.marker_unreadable":
    "La nota di un aggiornamento interrotto non si poteva leggere. È stata rimossa.",
  "updates.applyMsg.marker_outside":
    "La nota di un aggiornamento interrotto indica un file fuori dalla cartella di stato. È stata rimossa.",
  "updates.applyMsg.boot_not_confirmed":
    "La versione {version} non è stata confermata dopo {count} avvii della macchina con essa. È stata rimessa la versione {from}.{note}",
  "updates.applyMsg.boot_not_confirmed_bad":
    "La versione {version} non è stata confermata dopo {count} avvii della macchina con essa, e rimettere la {from} non è riuscito: {error}",
  "updates.applyMsg.boot_unusable":
    "L'aggiornamento è stato interrotto e il programma rimasto non è utilizzabile. È stata rimessa la versione {from}.{note}",
  "updates.applyMsg.boot_unusable_bad":
    "L'aggiornamento è stato interrotto e il programma rimasto non è utilizzabile, e rimettere la {from} non è riuscito: {error}",
  "updates.applyMsg.boot_database_bad":
    "L'aggiornamento è stato interrotto. Resta la {from}, ma il database non si è potuto ripristinare: {error}",
  "updates.applyMsg.boot_database_back":
    "L'aggiornamento è stato interrotto. Resta la versione {from}.{note}",
  "updates.applyMsg.policy_channel":
    "La versione {version} è sul canale {channel}, e la politica di aggiornamento di questo server ammette al massimo {max}.",
  "updates.applyMsg.policy_major":
    "La versione {version} è una nuova versione maggiore, e la politica di aggiornamento di questo server ammette al massimo la {max}.",
  "updates.applyMsg.policy_unreadable":
    "La politica di aggiornamento locale non è utilizzabile, quindi non viene installato nulla: {error}",

  // #368: the message of a server's step in an update of the servers
  // (internal/hub/rollout.go).
  "updates.rolloutMsg.not_connected": "Il server non è collegato.",
  "updates.rolloutMsg.no_archive":
    "L'hub non ha un archivio per questo server: {error}",
  "updates.rolloutMsg.stopped": "Fermato.",
  "updates.rolloutMsg.agent_too_old":
    "Questo agente è troppo vecchio per aggiornarsi da solo: aggiornalo una volta con install.sh.",
  "updates.rolloutMsg.result_unknown":
    "L'aggiornamento dei server è stato fermato prima di conoscere il risultato di questo server.",
  "updates.rolloutMsg.rolled_back":
    "Il server si è collegato con la versione {version}, poi è tornato con la versione {back}: l'installazione ha annullato l'aggiornamento.",
  "updates.rolloutMsg.did_not_take":
    "Il server è tornato con la versione {back} e non con la {version} entro {wait}: l'aggiornamento non ha avuto effetto o è stato annullato.",
  "updates.rolloutMsg.did_not_stay":
    "Il server si è collegato con la versione {version} ma non è rimasto collegato.",
  "updates.rolloutMsg.did_not_return":
    "Il server non si è ricollegato entro {wait}.",
};
