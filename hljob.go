package main

// hljob.go — resaltado progresivo de archivos grandes.
//
// chroma tokeniza a ~0,6 MB/s: un .go de 5 MB eran 8 s con la ventana esperando. Ahora /render
// resalta de entrada solo lo que entra en syncBudget (las primeras pantallas), manda el resto PLANO
// en el mismo HTML (misma grilla de renglones: nada se mueve) y deja un trabajo que sigue
// tokenizando con el MISMO iterador —el estado del lexer queda intacto, asi un comentario de varias
// lineas que cruza de un bloque a otro se pinta bien— y avisa por el bus cada tanto. La pagina pide
// esos bloques (/api/hl) y los reemplaza en su lugar.
//
// Vida de un trabajo: nace con cada /render que no alcanzo a resaltar todo; muere cuando termina y
// la pagina se llevo todos sus bloques, cuando llega un /render nuevo del mismo archivo, cuando la
// pestaña se cierra (el archivo sale de /api/watch) o, como red de seguridad, a los jobTTL.

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	publishEvery = 120 * time.Millisecond // cada cuanto se avisa a la pagina (no un mensaje por bloque)
	jobTTL       = 10 * time.Minute       // tope de vida: un trabajo huerfano no retiene memoria
	jobGrace     = 5 * time.Second        // lo que tarda una pestaña nueva en figurar en /api/watch
)

type hlJob struct {
	id     uint64
	key    string // archivo (ruta normalizada); "" para el texto soltado en la ventana
	ctx    context.Context
	cancel context.CancelFunc
	born   time.Time

	mu       sync.Mutex
	ready    map[int]string // bloque -> HTML resaltado, hasta que la pagina lo pide
	total    int
	finished bool
}

var jobs = struct {
	sync.Mutex
	seq   uint64
	byID  map[uint64]*hlJob
	byKey map[string]*hlJob
}{byID: map[uint64]*hlJob{}, byKey: map[string]*hlJob{}}

// jobSlots acota los trabajos que tokenizan a la vez: es CPU pura y abrir diez archivos grandes
// juntos no puede tomar la maquina entera (los demas esperan su turno).
var jobSlots = make(chan struct{}, max(1, runtime.NumCPU()/2))

// startHighlightJob sigue escribiendo los bloques [from, total) con w en segundo plano. Cancela
// el trabajo anterior del mismo archivo (su HTML ya no sirve).
func startHighlightJob(key string, w *lineWriter, from, total int) *hlJob {
	ctx, cancel := context.WithCancel(context.Background())
	job := &hlJob{key: key, ctx: ctx, cancel: cancel, born: time.Now(), ready: map[int]string{}, total: total}

	jobs.Lock()
	jobs.seq++
	job.id = jobs.seq
	if key != "" {
		if old := jobs.byKey[key]; old != nil {
			dropJobLocked(old)
		}
		jobs.byKey[key] = job
	}
	jobs.byID[job.id] = job
	sweepLocked()
	jobs.Unlock()

	go job.run(w, from)
	return job
}

func (job *hlJob) run(w *lineWriter, from int) {
	select {
	case jobSlots <- struct{}{}:
		defer func() { <-jobSlots }()
	case <-job.ctx.Done():
		return
	}
	var b strings.Builder
	published, last := from, time.Now()
	for c := from; c < job.total; c++ {
		if job.ctx.Err() != nil {
			return
		}
		b.Reset()
		w.writeChunk(&b, c)
		job.mu.Lock()
		job.ready[c] = b.String()
		if c+1 == job.total {
			job.finished = true
		}
		job.mu.Unlock()
		if c+1 == job.total || time.Since(last) >= publishEvery {
			broadcastBus(fmt.Sprintf("hl\t%d\t%d\t%d", job.id, published, c+1))
			published, last = c+1, time.Now()
		}
	}
}

// takeChunks entrega (y olvida) los bloques ya resaltados a partir de from, en orden y sin
// huecos: lo que todavia no esta se pide en el proximo aviso. Devuelve nil si el trabajo no existe.
func takeChunks(id uint64, from int) (chunks []string, done bool, ok bool) {
	jobs.Lock()
	job := jobs.byID[id]
	jobs.Unlock()
	if job == nil {
		return nil, false, false
	}
	job.mu.Lock()
	for c := from; ; c++ {
		html, has := job.ready[c]
		if !has {
			break
		}
		chunks = append(chunks, html)
		delete(job.ready, c)
	}
	done = job.finished && len(job.ready) == 0 && from+len(chunks) >= job.total
	job.mu.Unlock()
	if done { // la pagina ya tiene todo: el trabajo no tiene mas razon de existir
		jobs.Lock()
		dropJobLocked(job)
		jobs.Unlock()
	}
	return chunks, done, true
}

// cancelJobsExcept cancela los trabajos de archivos que ya no estan abiertos (pestañas cerradas).
// Un trabajo recien nacido se respeta: su pestaña se crea cuando llega la respuesta de /render, y
// una sincronizacion de pestañas que se cruce en ese instante no lo tiene todavia en la lista.
func cancelJobsExcept(open map[string]bool) {
	jobs.Lock()
	defer jobs.Unlock()
	for key, job := range jobs.byKey {
		if !open[key] && time.Since(job.born) > jobGrace {
			dropJobLocked(job)
		}
	}
}

func dropJobLocked(job *hlJob) {
	job.cancel()
	delete(jobs.byID, job.id)
	if jobs.byKey[job.key] == job {
		delete(jobs.byKey, job.key)
	}
}

// sweepLocked: red de seguridad contra trabajos huerfanos (la pagina se recargo, etc.).
func sweepLocked() {
	for _, job := range jobs.byID {
		if time.Since(job.born) > jobTTL {
			dropJobLocked(job)
		}
	}
}
