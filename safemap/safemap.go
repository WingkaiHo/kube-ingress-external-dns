package safemap

type SafeMap interface {
	Insert(string, interface{})
	Delete(string)
	Find(string) (interface{}, bool)
	Len() int
	Close() map[string]interface{}
	Update(string, UpdateFunc)
	Dump(DumpFunc) []interface{}
}

type UpdateFunc func(interface{}, bool) interface{}

type DumpFunc func(map[string]interface{}) []interface{}

type commandAction int

const (
	remove commandAction = iota
	insert
	find
	length
	end
	update
	dump
)

type findResult struct {
	find  bool
	value interface{}
}

type commandData struct {
	action  commandAction
	key     string
	value   interface{}
	reply   chan<- interface{}
	data    chan<- map[string]interface{}
	output  chan<- []interface{}
	updater UpdateFunc
	dumper  DumpFunc
}

type safeMap chan commandData

func (sm safeMap) run() {
	store := make(map[string]interface{})

	for command := range sm {
		switch command.action {
		case remove:
			delete(store, command.key)
		case insert:
			store[command.key] = command.value
		case find:
			value, found := store[command.key]
			command.reply <- findResult{find: found, value: value}
		case length:
			command.reply <- len(store)
		case update:
			value, found := store[command.key]
			store[command.key] = command.updater(value, found)
		case dump:
			if command.dumper != nil {
				buf := command.dumper(store)
				command.output <- buf
			}
		case end:
			close(sm)
			command.data <- store
		}
	}
}

func New() SafeMap {
	sm := make(safeMap)
	go sm.run()
	return sm
}

func (sm safeMap) Delete(key string) {
	sm <- commandData{action: remove, key: key}
}

func (sm safeMap) Insert(key string, value interface{}) {
	sm <- commandData{action: insert, key: key, value: value}
}

func (sm safeMap) Find(key string) (interface{}, bool) {
	reply := make(chan interface{})
	sm <- commandData{action: find, key: key, reply: reply}
	result := (<-reply).(findResult)
	return result.value, result.find
}

func (sm safeMap) Len() int {
	reply := make(chan interface{})
	sm <- commandData{action: length, reply: reply}
	return (<-reply).(int)
}

func (sm safeMap) Close() map[string]interface{} {
	reply := make(chan map[string]interface{})
	sm <- commandData{action: end, data: reply}
	return <-reply
}

func (sm safeMap) Update(key string, updater UpdateFunc) {
	sm <- commandData{action: update, updater: updater, key: key}
}

func (sm safeMap) Dump(dumper DumpFunc) []interface{} {
	output := make(chan []interface{})
	sm <- commandData{action: dump, dumper: dumper, output: output}
	return <-output
}
