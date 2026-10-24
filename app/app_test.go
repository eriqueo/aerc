package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"git.sr.ht/~rjarry/aerc/config"
	"git.sr.ht/~rjarry/aerc/lib"
	"git.sr.ht/~rjarry/aerc/lib/ui"
	"git.sr.ht/~rjarry/aerc/models"
	"git.sr.ht/~rjarry/aerc/worker/types"
)

func TestMainThreadIPCHandlerQueuesCommand(t *testing.T) {
	wantArgs := []string{":reload"}
	wantErr := errors.New("command result")
	queued := make(chan func(), 1)
	called := make(chan []string, 1)

	handler := mainThreadIPCHandler{
		queue: func(callback func()) {
			queued <- callback
		},
		command: func(args []string) error {
			called <- args
			return wantErr
		},
	}

	result := make(chan error, 1)
	go func() {
		result <- handler.Command(wantArgs)
	}()

	var callback func()
	select {
	case callback = <-queued:
	case <-time.After(time.Second):
		t.Fatal("IPC command was not queued")
	}

	select {
	case <-called:
		t.Fatal("IPC command ran before the main-loop callback")
	default:
	}

	callback()

	select {
	case gotArgs := <-called:
		if !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Fatalf("command args = %q, want %q", gotArgs, wantArgs)
		}
	case <-time.After(time.Second):
		t.Fatal("queued IPC command did not run")
	}

	select {
	case gotErr := <-result:
		if !errors.Is(gotErr, wantErr) {
			t.Fatalf("command error = %v, want %v", gotErr, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("IPC handler did not return the command result")
	}
}

func TestIPCHandlerUsesMainThreadQueue(t *testing.T) {
	if _, ok := IPCHandler().(mainThreadIPCHandler); !ok {
		t.Fatal("IPCHandler bypasses mainThreadIPCHandler")
	}
}

// Selection tests use the real action queue and response callback owner. Only
// the backend is withheld: the test chooses when an OpenDirectory completes.
func selectionAccount(t *testing.T) (*AccountView, *DirectoryList) {
	t.Helper()
	oldTabs, oldDelay := aerc.tabs, config.Ui.DirListDelay
	aerc.tabs = ui.NewTabs(func(ui.Drawable) *config.UIConfig { return config.Ui })
	config.Ui.DirListDelay = 0
	worker := types.NewWorker("synthetic")
	worker.Backend = selectionBackend{}
	dirs := &DirectoryList{
		acctConf: &config.AccountConfig{Name: "synthetic", Default: "Inbox"},
		store:    lib.NewDirStore(), worker: worker,
		spinner: &Spinner{frame: -1},
	}
	dirs.NewContext()
	for _, name := range []string{"Inbox", "thread:0000000000000001", "thread:0000000000000002"} {
		store := lib.NewMessageStore(worker, name,
			func() *config.UIConfig { return config.Ui }, nil, nil, nil, nil, nil, nil, nil)
		dirs.SetMsgStore(&models.Directory{Name: name}, store)
	}
	acct := &AccountView{
		acct: dirs.acctConf, worker: worker, dirlist: dirs,
		msglist: &MessageList{spinner: &Spinner{frame: -1}, isInitalizing: true},
	}
	t.Cleanup(func() {
		dirs.cancel()
		// A FIFO sentinel drains sorting actions and their callbacks without
		// leaving the real worker queue's sender blocked after the test.
		end := &types.Disconnect{}
		worker.PostAction(end, nil)
		deadline := time.After(time.Second)
		for {
			select {
			case callback := <-ui.Callbacks:
				callback()
			case action := <-worker.Actions():
				if action == end {
					aerc.tabs, config.Ui.DirListDelay = oldTabs, oldDelay
					return
				}
				worker.ProcessMessage(&types.Cancelled{Message: types.RespondTo(action)})
			case <-deadline:
				t.Error("test worker queue did not drain")
				aerc.tabs, config.Ui.DirListDelay = oldTabs, oldDelay
				return
			}
		}
	})
	return acct, dirs
}

func selectionAction(t *testing.T, worker *types.Worker) *types.OpenDirectory {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case callback := <-ui.Callbacks:
			callback()
		case action := <-worker.Actions():
			if open, ok := action.(*types.OpenDirectory); ok {
				return open
			}
			// Complete incidental sorting through the real callback retirement.
			worker.ProcessMessage(&types.Done{Message: types.RespondTo(action)})
		case <-deadline:
			t.Fatal("selection did not issue an OpenDirectory action")
		}
	}
}

func assertOpenRetired(t *testing.T, worker *types.Worker, action *types.OpenDirectory) {
	t.Helper()
	// No new diagnostic API is needed for this resource-lifecycle assertion.
	id := reflect.ValueOf(action).Elem().FieldByName("Message").FieldByName("id").Int()
	worker.Lock()
	registered := reflect.ValueOf(worker).Elem().FieldByName("actionCallbacks").MapIndex(reflect.ValueOf(id)).IsValid()
	worker.Unlock()
	if registered {
		t.Fatal("terminal OpenDirectory callback was not retired")
	}
}

func TestPendingQuerySurvivesInitialDirectoryListing(t *testing.T) {
	acct, dirs := selectionAccount(t)
	const query = "thread:0000000000000001"
	dirs.Open(query, query, 0, nil, true) // Query.Execute's actual production seam.
	open := selectionAction(t, acct.worker)
	acct.onMessage(&types.Done{Message: types.RespondTo(&types.ListDirectories{})})
	if open.Context.Err() != nil {
		t.Fatal("initial listing canceled the explicit query and scheduled the default folder")
	}
	acct.onMessage(&types.Done{Message: types.RespondTo(open)})
	if dirs.Selected() != query {
		t.Fatalf("selected %q, want explicit query %q", dirs.Selected(), query)
	}
	assertOpenRetired(t, acct.worker, open)
}

func TestStaleDefaultCompletionCannotReplaceQueryOrRunCallback(t *testing.T) {
	acct, dirs := selectionAccount(t)
	oldCallbacks, currentCallbacks := 0, 0
	dirs.Open("Inbox", "", 0, func(types.WorkerMessage) { oldCallbacks++ }, false)
	old := selectionAction(t, acct.worker)
	const query = "thread:0000000000000001"
	dirs.Open(query, query, 0, func(types.WorkerMessage) { currentCallbacks++ }, true)
	current := selectionAction(t, acct.worker)
	if !errors.Is(old.Context.Err(), context.Canceled) {
		t.Fatal("new query did not cancel the older default context")
	}
	// The backend may have passed its cancellation check before the newer
	// intent. Completion still must not run old callbacks or select Inbox.
	acct.onMessage(&types.Done{Message: types.RespondTo(old)})
	if oldCallbacks != 0 || dirs.Selected() == "Inbox" {
		t.Fatalf("stale completion selected %q, callback calls=%d", dirs.Selected(), oldCallbacks)
	}
	assertOpenRetired(t, acct.worker, old)
	acct.onMessage(&types.Done{Message: types.RespondTo(current)})
	if currentCallbacks != 1 || dirs.Selected() != query {
		t.Fatalf("current completion selected %q, callback calls=%d", dirs.Selected(), currentCallbacks)
	}
	assertOpenRetired(t, acct.worker, current)
	acct.worker.ProcessMessage(&types.Done{Message: types.RespondTo(current)})
	if currentCallbacks != 1 {
		t.Fatal("retired callback ran twice")
	}
}

func TestNormalStartupStillOpensDefaultDirectory(t *testing.T) {
	acct, dirs := selectionAccount(t)
	acct.onMessage(&types.Done{Message: types.RespondTo(&types.ListDirectories{})})
	open := selectionAction(t, acct.worker)
	if open.Directory != "Inbox" || open.Query != "" || open.Context.Err() != nil {
		t.Fatalf("normal startup action: %+v", open)
	}
	acct.onMessage(&types.Done{Message: types.RespondTo(open)})
	if dirs.Selected() != "Inbox" {
		t.Fatalf("normal startup selected %q", dirs.Selected())
	}
	assertOpenRetired(t, acct.worker, open)
}

func TestReadyDefaultDispatchCannotSupersedeNewQuery(t *testing.T) {
	acct, dirs := selectionAccount(t)
	dirs.Open("Inbox", "", 0, nil, false)
	var dispatch func()
	select {
	case dispatch = <-ui.Callbacks:
	case <-time.After(time.Second):
		t.Fatal("default timer did not queue its UI dispatch")
	}
	// The old timer is ready, but its dispatch has not run on the UI loop.
	// A new explicit query cancels it before that decision point.
	const query = "thread:0000000000000001"
	dirs.Open(query, query, 0, nil, true)
	dispatch()
	open := selectionAction(t, acct.worker)
	if open.Directory != query || open.Context.Err() != nil {
		t.Fatalf("old default dispatched after query intent: %+v", open)
	}
	acct.onMessage(&types.Done{Message: types.RespondTo(open)})
	assertOpenRetired(t, acct.worker, open)
}

func TestSameFolderForcedQueryKeepsItsPendingContext(t *testing.T) {
	acct, dirs := selectionAccount(t)
	const name = "thread:0000000000000001"
	dirs.Open(name, "tag:old", 0, nil, true)
	old := selectionAction(t, acct.worker)
	dirs.Open(name, "tag:new", 0, nil, true)
	current := selectionAction(t, acct.worker)
	acct.onMessage(&types.Done{Message: types.RespondTo(&types.ListDirectories{})})
	if current.Context.Err() != nil || current.Query != "tag:new" || !current.Force {
		t.Fatal("folder listing replaced the forced query's current context")
	}
	acct.onMessage(&types.Done{Message: types.RespondTo(old)})
	assertOpenRetired(t, acct.worker, old)
	acct.onMessage(&types.Done{Message: types.RespondTo(current)})
	assertOpenRetired(t, acct.worker, current)
	if dirs.Selected() != name {
		t.Fatalf("selected %q, want %q", dirs.Selected(), name)
	}
}

func TestStaleCancellationDoesNotClearNewSelection(t *testing.T) {
	acct, dirs := selectionAccount(t)
	callbacks := 0
	dirs.Open("Inbox", "", 0, func(types.WorkerMessage) { callbacks++ }, false)
	old := selectionAction(t, acct.worker)
	const query = "thread:0000000000000001"
	dirs.Open(query, query, 0, nil, true)
	current := selectionAction(t, acct.worker)
	acct.onMessage(&types.Cancelled{Message: types.RespondTo(old)})
	if callbacks != 0 || dirs.selecting != query || !dirs.IsOpening() {
		t.Fatal("stale cancellation changed the newer selection")
	}
	assertOpenRetired(t, acct.worker, old)
	acct.onMessage(&types.Done{Message: types.RespondTo(current)})
	assertOpenRetired(t, acct.worker, current)
}

// No account/backend is started; only the directory tree's separator is used.
type selectionBackend struct{}

func (selectionBackend) Run()                               { panic("selection tests must not start a backend") }
func (selectionBackend) Capabilities() *models.Capabilities { return &models.Capabilities{} }
func (selectionBackend) PathSeparator() string              { return "/" }

func TestDirectoryTreePreservesPendingQueryAndRetiresStaleCallback(t *testing.T) {
	acct, dirs := selectionAccount(t)
	acct.dirlist = NewDirectoryTree(dirs)
	staleCalls := 0
	acct.dirlist.Open("Inbox", "", 0, func(types.WorkerMessage) { staleCalls++ }, false)
	old := selectionAction(t, acct.worker)
	const query = "thread:0000000000000001"
	acct.dirlist.Open(query, query, 0, nil, true)
	current := selectionAction(t, acct.worker)
	acct.onMessage(&types.Done{Message: types.RespondTo(&types.ListDirectories{})})
	if current.Context.Err() != nil {
		t.Fatal("tree listing canceled pending query")
	}
	acct.onMessage(&types.Done{Message: types.RespondTo(old)})
	if staleCalls != 0 || acct.dirlist.Selected() == "Inbox" {
		t.Fatal("stale tree callback/selection applied")
	}
	assertOpenRetired(t, acct.worker, old)
	acct.onMessage(&types.Done{Message: types.RespondTo(current)})
	want, _ := dirs.MsgStore(query)
	if acct.dirlist.Selected() != query || acct.Store() != want {
		t.Fatal("tree query did not become the selected store")
	}
	assertOpenRetired(t, acct.worker, current)
}

func TestDirectoryTreeNormalStartupAndLaterNavigation(t *testing.T) {
	acct, dirs := selectionAccount(t)
	acct.dirlist = NewDirectoryTree(dirs)
	acct.onMessage(&types.Done{Message: types.RespondTo(&types.ListDirectories{})})
	initial := selectionAction(t, acct.worker)
	if initial.Directory != "Inbox" {
		t.Fatalf("tree default = %q", initial.Directory)
	}
	acct.onMessage(&types.Done{Message: types.RespondTo(initial)})
	const query = "thread:0000000000000002"
	acct.dirlist.Open(query, query, 0, nil, true)
	next := selectionAction(t, acct.worker)
	acct.onMessage(&types.Done{Message: types.RespondTo(next)})
	if acct.dirlist.Selected() != query || dirs.Previous() != "Inbox" || dirs.IsOpening() {
		t.Fatal("tree navigation state was not preserved")
	}
	assertOpenRetired(t, acct.worker, initial)
	assertOpenRetired(t, acct.worker, next)
}

func TestPendingNavigationDoesNotRestartStartupState(t *testing.T) {
	acct, dirs := selectionAccount(t)
	acct.onMessage(&types.Done{Message: types.RespondTo(&types.ListDirectories{})})
	initial := selectionAction(t, acct.worker)
	acct.onMessage(&types.Done{Message: types.RespondTo(initial)})
	acct.newConn = false // Startup mail check has already completed.
	const query = "thread:0000000000000001"
	dirs.Open(query, query, 0, nil, true)
	current := selectionAction(t, acct.worker)
	acct.onMessage(&types.Done{Message: types.RespondTo(&types.ListDirectories{})})
	if acct.newConn || acct.msglist.isInitalizing || current.Context.Err() != nil {
		t.Fatal("listing during later navigation restarted startup state")
	}
	acct.onMessage(&types.Done{Message: types.RespondTo(current)})
	assertOpenRetired(t, acct.worker, current)
}

func TestVirtualDirectoryContextCancelsPendingOpen(t *testing.T) {
	acct, dirs := selectionAccount(t)
	dirs.Open("Inbox", "", 0, nil, false)
	old := selectionAction(t, acct.worker)
	// DirectoryTree.selectIndex uses NewContext without Open for virtual nodes.
	dirs.NewContext()
	if dirs.IsOpening() || old.Context.Err() == nil {
		t.Fatal("virtual directory kept the canceled open marked pending")
	}
	acct.onMessage(&types.Cancelled{Message: types.RespondTo(old)})
	assertOpenRetired(t, acct.worker, old)
}
