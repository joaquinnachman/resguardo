package main

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const AppName = "Resguardo"

// AppVersion es var (y no const) para poder compilar versiones de prueba con -ldflags -X.
var AppVersion = "1.1.1"

// ---------- Tareas ----------

const (
	TypeFull     = "completo"
	TypeIncr     = "incremental"
	TypeDiff     = "diferencial"
	TypeDummy    = "ficticio"
	TypeProjects = "proyectos"
)

type Location struct {
	Kind       string `json:"kind"` // local | ftp | sftp
	Path       string `json:"path"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	User       string `json:"user,omitempty"`
	Pass       string `json:"pass,omitempty"`
	TLS        string `json:"tls,omitempty"` // no | explicito | implicito
	Passive    bool   `json:"passive,omitempty"`
	KeyFile    string `json:"keyFile,omitempty"`
	ForceDir   bool   `json:"forceDir,omitempty"`  // ?D? : tratar como carpeta aunque no exista todavía
	ForceFile  bool   `json:"forceFile,omitempty"` // ?F? : tratar como archivo aunque no exista todavía
	IgnoreCert bool   `json:"ignoreCert,omitempty"`
}

type Schedule struct {
	Kind         string `json:"kind"` // manual | unavez | diario | semanal | mensual | anual | temporizador | inicio
	Time         string `json:"time"` // HH:MM
	Date         string `json:"date"` // YYYY-MM-DD (una vez)
	Weekdays     []int  `json:"weekdays"`
	MonthDay     int    `json:"monthDay"`
	Month        int    `json:"month"`
	EveryMinutes int    `json:"everyMinutes"`
	AtStartup    bool   `json:"atStartup"`  // además, correr al iniciar el programa
	OnDAWClose   bool   `json:"onDawClose"` // además, correr cuando se cierra un DAW
	OnDAWSave    bool   `json:"onDawSave"`  // además, correr cuando se guarda un proyecto en los orígenes
	WindowFrom   string `json:"windowFrom"` // HH:MM opcional: sólo dentro de esta franja
	WindowTo     string `json:"windowTo"`
}

type FilterItem struct {
	Kind  string `json:"kind"` // mascara | archivo | carpeta | regex
	Value string `json:"value"`
}

type DateFilter struct {
	Mode string `json:"mode"` // "" | incluir | excluir
	Op   string `json:"op"`   // mas_nuevos | mas_viejos
	Date string `json:"date"` // YYYY-MM-DD, o "hoy", "ayer", "manana", o "-N" días
}

type SizeFilter struct {
	Mode string  `json:"mode"` // "" | incluir | excluir
	Op   string  `json:"op"`   // mayores | menores
	MB   float64 `json:"mb"`
}

type Event struct {
	Kind    string `json:"kind"` // ejecutar | ejecutar_esperar | cerrar_programa | iniciar_servicio | detener_servicio | pausa | apagar | reiniciar | hibernar | suspender | cerrar_sesion
	Target  string `json:"target"`
	Args    string `json:"args"`
	Seconds int    `json:"seconds"`
}

type RunAs struct {
	Enabled       bool   `json:"enabled"`
	User          string `json:"user"`
	Domain        string `json:"domain"`
	Password      string `json:"password"`
	CancelIfFails bool   `json:"cancelIfFails"`
}

type ProjectOverride struct {
	Destinations []Location `json:"destinations"`
	Excluded     bool       `json:"excluded"`
	Note         string     `json:"note"`
}

type ProjectsConfig struct {
	DAWs            []string                   `json:"daws"`
	TriggerOnSave   bool                       `json:"triggerOnSave"`
	TriggerOnClose  bool                       `json:"triggerOnClose"`
	IncludeExternal bool                       `json:"includeExternal"`
	IncludeFactory  bool                       `json:"includeFactory"`
	SmartExclusions bool                       `json:"smartExclusions"`
	KeepVersions    int                        `json:"keepVersions"` // 0 = todas
	KeepDays        int                        `json:"keepDays"`     // 0 = sin límite
	Thinning        bool                       `json:"thinning"`
	Verify          bool                       `json:"verify"`
	MaxDepth        int                        `json:"maxDepth"`
	Overrides       map[string]ProjectOverride `json:"overrides"`
}

type Task struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Group   string `json:"group"`
	Type    string `json:"type"`

	IncludeSubdirs bool `json:"includeSubdirs"`
	Separated      bool `json:"separated"`
	Direct         bool `json:"direct"` // sin carpeta de la tarea: cada origen queda tal cual dentro del destino
	UseArchiveAttr bool `json:"useArchiveAttr"`
	UseVSS         bool `json:"useVSS"`

	Sources      []Location `json:"sources"`
	Destinations []Location `json:"destinations"`

	Schedule Schedule `json:"schedule"`

	Priority         int `json:"priority"` // -1 baja, 0 normal, 1 alta
	FullCopiesToKeep int `json:"fullCopiesToKeep"`
	FullEvery        int `json:"fullEvery"`
	DiffCopiesToKeep int `json:"diffCopiesToKeep"`
	FixedFullWeekday int `json:"fixedFullWeekday"` // -1 ninguno, 0=domingo..6

	Compression string `json:"compression"` // ninguna | individual | separado | global
	Format      string `json:"format"`      // zip | 7z
	Level       int    `json:"level"`
	Comment     string `json:"comment"`
	Encrypt     bool   `json:"encrypt"`
	Password    string `json:"password"`
	SplitMB     int    `json:"splitMB"`

	Include    []FilterItem `json:"include"`
	Exclude    []FilterItem `json:"exclude"`
	DateFilter DateFilter   `json:"dateFilter"`
	SizeFilter SizeFilter   `json:"sizeFilter"`

	PreEvents           []Event `json:"preEvents"`
	PostEvents          []Event `json:"postEvents"`
	CancelIfPreFails    bool    `json:"cancelIfPreFails"`
	SkipPostIfPreFailed bool    `json:"skipPostIfPreFailed"`

	ClearArchiveAttr bool  `json:"clearArchiveAttr"`
	Mirror           bool  `json:"mirror"`
	AbsolutePaths    bool  `json:"absolutePaths"`
	TopParentDir     bool  `json:"topParentDir"`
	TypeInName       bool  `json:"typeInName"`
	IgnoreEmptyDirs  bool  `json:"ignoreEmptyDirs"`
	RunAs            RunAs `json:"runAs"`

	Projects ProjectsConfig `json:"projects"`

	Created time.Time `json:"created"`
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func NewTask() Task {
	t := Task{
		ID: newID(), Name: "Nueva tarea", Enabled: true, Type: TypeIncr,
		IncludeSubdirs: true, Separated: true,
		Schedule:         Schedule{Kind: "diario", Time: "22:00", Weekdays: []int{1, 2, 3, 4, 5}, MonthDay: 1, Month: 1, EveryMinutes: 60},
		FixedFullWeekday: -1, FullEvery: 0, FullCopiesToKeep: 3,
		Compression: "ninguna", Format: "zip", Level: 6,
		ClearArchiveAttr: true, TypeInName: true, IgnoreEmptyDirs: false,
		CancelIfPreFails: true, SkipPostIfPreFailed: true,
		Projects: defaultProjectsConfig(),
		Created:  time.Now(),
	}
	fillSlices(&t)
	return t
}

// fillSlices evita que la interfaz reciba null en lugar de listas vacías.
func fillSlices(t *Task) {
	if t.Sources == nil {
		t.Sources = []Location{}
	}
	if t.Destinations == nil {
		t.Destinations = []Location{}
	}
	if t.Include == nil {
		t.Include = []FilterItem{}
	}
	if t.Exclude == nil {
		t.Exclude = []FilterItem{}
	}
	if t.PreEvents == nil {
		t.PreEvents = []Event{}
	}
	if t.PostEvents == nil {
		t.PostEvents = []Event{}
	}
	if t.Schedule.Weekdays == nil {
		t.Schedule.Weekdays = []int{}
	}
	if t.Projects.DAWs == nil {
		t.Projects.DAWs = []string{}
	}
	if t.Projects.Overrides == nil {
		t.Projects.Overrides = map[string]ProjectOverride{}
	}
	for k, o := range t.Projects.Overrides {
		if o.Destinations == nil {
			o.Destinations = []Location{}
			t.Projects.Overrides[k] = o
		}
	}
}

func defaultProjectsConfig() ProjectsConfig {
	return ProjectsConfig{
		DAWs:            []string{"ableton", "cubase", "protools"},
		TriggerOnSave:   true,
		TriggerOnClose:  true,
		IncludeExternal: true,
		SmartExclusions: true,
		Thinning:        true,
		MaxDepth:        6,
		Overrides:       map[string]ProjectOverride{},
	}
}

// ---------- Opciones generales ----------

type SMTPSettings struct {
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	Security   string   `json:"security"` // ninguna | starttls | ssl
	Auth       bool     `json:"auth"`
	User       string   `json:"user"`
	Pass       string   `json:"pass"`
	From       string   `json:"from"`
	FromName   string   `json:"fromName"`
	To         []string `json:"to"`
	Subject    string   `json:"subject"`
	Body       string   `json:"body"`
	IgnoreCert bool     `json:"ignoreCert"`
	TimeoutMs  int      `json:"timeoutMs"`
}

type Settings struct {
	CurrentList string `json:"currentList"`

	// General
	RunAllAtStartup bool   `json:"runAllAtStartup"`
	TempDir         string `json:"tempDir"`
	Autostart       bool   `json:"autostart"`
	Hotkey          bool   `json:"hotkey"` // Ctrl+Alt+R abre la ventana (Windows)

	// Registro
	LogDetailed        bool `json:"logDetailed"`
	LogPerDay          bool `json:"logPerDay"`
	LogIndividualFiles bool `json:"logIndividualFiles"`
	DeleteLogsDays     int  `json:"deleteLogsDays"`
	ClearLogOnStart    bool `json:"clearLogOnStart"`

	// Correo
	MailEnabled      bool         `json:"mailEnabled"`
	MailWhen         string       `json:"mailWhen"` // tarea | respaldo | diario
	MailTime         string       `json:"mailTime"`
	MailOnlyErrors   bool         `json:"mailOnlyErrors"`
	MailAttach       bool         `json:"mailAttach"`
	MailDeleteIfSent bool         `json:"mailDeleteIfSent"`
	MailLatestOnly   bool         `json:"mailLatestOnly"`
	SMTP             SMTPSettings `json:"smtp"`

	// Archivo comprimido
	Zip64              bool   `json:"zip64"`
	UnicodeNames       bool   `json:"unicodeNames"`
	ZipEncryption      string `json:"zipEncryption"` // aes256 | aes128 | zipcrypto
	StoredExtensions   string `json:"storedExtensions"`
	TestArchives       bool   `json:"testArchives"`
	AbsPathsInArchives bool   `json:"absPathsInArchives"`

	// Visual
	Theme           string `json:"theme"` // auto | oscuro | claro
	PlaySound       bool   `json:"playSound"`
	SoundFile       string `json:"soundFile"`
	TrayErrorSignal bool   `json:"trayErrorSignal"`
	HistoryItems    int    `json:"historyItems"`

	// Funcionamiento / motor
	RunMissed            bool `json:"runMissed"`
	NoDuplicateQueue     bool `json:"noDuplicateQueue"`
	FirstBackupFull      bool `json:"firstBackupFull"`
	PreventSleep         bool `json:"preventSleep"`
	CancelIfVSSFails     bool `json:"cancelIfVSSFails"`
	ExactProgress        bool `json:"exactProgress"`
	LowPriority          bool `json:"lowPriority"`
	NoRunOnBattery       bool `json:"noRunOnBattery"`
	CopyAttributes       bool `json:"copyAttributes"`
	CopyTimestamps       bool `json:"copyTimestamps"`
	CopyPermissions      bool `json:"copyPermissions"`
	IgnoreAttrErrors     bool `json:"ignoreAttrErrors"`
	IgnoreReparsePoints  bool `json:"ignoreReparsePoints"`
	SaveEmptyBackups     bool `json:"saveEmptyBackups"`
	ParkFirstBackup      bool `json:"parkFirstBackup"`
	NeverIgnoreEmptyFull bool `json:"neverIgnoreEmptyFull"`
	MaxSpeedMBps         int  `json:"maxSpeedMBps"`

	// Seguridad / remoto
	ProtectUI      bool   `json:"protectUI"`
	UIPasswordHash string `json:"uiPasswordHash"`
	RemoteAccess   bool   `json:"remoteAccess"`
	Port           int    `json:"port"`

	// Avanzado
	TimestampFormat    string `json:"timestampFormat"`
	ProcessExitTimeout int    `json:"processExitTimeout"`
	FTPReconnect       bool   `json:"ftpReconnect"`
	SafeMirror         bool   `json:"safeMirror"`
	SafeMirrorDir      string `json:"safeMirrorDir"`
	WatchSafeMirror    bool   `json:"watchSafeMirror"`
	SafeMirrorWarnMB   int    `json:"safeMirrorWarnMB"`

	// Música
	DAWWatch            bool     `json:"dawWatch"`
	SaveQuietSeconds    int      `json:"saveQuietSeconds"`
	CloseDelaySeconds   int      `json:"closeDelaySeconds"`
	ThrottleWhileDAW    bool     `json:"throttleWhileDAW"`
	ThrottleMBps        int      `json:"throttleMBps"`
	RescanMinutes       int      `json:"rescanMinutes"`
	SampleSearchFolders []string `json:"sampleSearchFolders"`

	// Actualizaciones
	UpdateAuto    bool `json:"updateAuto"`    // buscar versiones nuevas
	UpdateInstall bool `json:"updateInstall"` // bajarlas e instalarlas solas

	Notes string `json:"notes"`
}

func DefaultSettings() Settings {
	return Settings{
		CurrentList:          "Principal",
		Autostart:            true,
		UpdateAuto:           true,
		UpdateInstall:        true,
		LogPerDay:            true,
		DeleteLogsDays:       60,
		MailWhen:             "tarea",
		MailTime:             "08:00",
		MailOnlyErrors:       true,
		SMTP:                 SMTPSettings{Port: 587, Security: "starttls", Auth: true, Subject: "Resguardo: registro de respaldos", TimeoutMs: 20000},
		Zip64:                true,
		UnicodeNames:         true,
		ZipEncryption:        "aes256",
		StoredExtensions:     "wav,aif,aiff,mp3,flac,ogg,m4a,aac,w64,rf64,caf,zip,7z,rar,jpg,jpeg,png,mp4,mov,mkv",
		TestArchives:         false,
		Theme:                "auto",
		TrayErrorSignal:      true,
		HistoryItems:         200,
		RunMissed:            true,
		NoDuplicateQueue:     true,
		FirstBackupFull:      true,
		PreventSleep:         true,
		ExactProgress:        true,
		LowPriority:          true,
		CopyAttributes:       true,
		CopyTimestamps:       true,
		IgnoreReparsePoints:  true,
		NeverIgnoreEmptyFull: false,
		Port:                 47321,
		TimestampFormat:      "yyyy-MM-dd HH;mm;ss",
		ProcessExitTimeout:   30,
		FTPReconnect:         true,
		SafeMirror:           true,
		SafeMirrorWarnMB:     5000,
		DAWWatch:             true,
		SaveQuietSeconds:     90,
		CloseDelaySeconds:    15,
		ThrottleWhileDAW:     true,
		ThrottleMBps:         40,
		RescanMinutes:        10,
	}
}

// ---------- Historial ----------

type Output struct {
	Dest int    `json:"dest"`
	Path string `json:"path"` // carpeta o archivo creado (ruta completa en el destino)
	Kind string `json:"kind"` // carpeta | zip | 7z | individual | espejo
	// Only: si no está vacío, Path es el destino compartido y el respaldo es
	// sólo estas carpetas o archivos dentro de él (modo directo).
	Only []string `json:"only,omitempty"`
}

type BackupRecord struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"taskId"`
	Type        string    `json:"type"` // completo | incremental | diferencial | ficticio | instantanea
	Started     time.Time `json:"started"`
	Finished    time.Time `json:"finished"`
	Status      string    `json:"status"` // ok | errores | cancelado | fallido
	Files       int       `json:"files"`
	Bytes       int64     `json:"bytes"`
	Errors      int       `json:"errors"`
	Parked      bool      `json:"parked"`
	Parent      string    `json:"parent,omitempty"` // id del completo del que depende
	Outputs     []Output  `json:"outputs"`
	Project     string    `json:"project,omitempty"` // clave del proyecto (tareas de proyectos)
	Snapshot    string    `json:"snapshot,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Note        string    `json:"note,omitempty"`
	Compression string    `json:"compression,omitempty"`
	Format      string    `json:"format,omitempty"`
	Encrypted   bool      `json:"encrypted,omitempty"`
}

// ---------- Estado de archivos para incremental / diferencial ----------

type FileStamp struct {
	Size  int64 `json:"s"`
	MTime int64 `json:"m"` // unix nanos
}

type TaskState struct {
	Last      map[string]FileStamp `json:"last"`
	Full      map[string]FileStamp `json:"full"`
	NextRun   time.Time            `json:"nextRun"`
	LastRun   time.Time            `json:"lastRun"`
	LastFull  time.Time            `json:"lastFull"`
	SinceFull int                  `json:"sinceFull"`
}
