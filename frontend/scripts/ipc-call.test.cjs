const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const ts = require('typescript')
const file = path.resolve(__dirname, '../src/shared/ipc/call.ts')
const compiled = ts.transpileModule(fs.readFileSync(file, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText
function client(byName, timers = []) {
 const mod = {exports:{}}
 const req = id => id === './runtime' ? {SVC:'svc',loadRuntime:async()=>({Call:{ByName:byName}})} : {mockCall:()=>{throw Error('unexpected mock')}}
 const set = (fn, ms) => { timers.push(ms); return setTimeout(fn, ms) }
 new Function('require','module','exports','setTimeout','clearTimeout',compiled)(req,mod,mod.exports,set,clearTimeout)
 return mod.exports
}
const tick = () => new Promise(resolve => setImmediate(resolve))

test('long mutation does not block an unrelated read or mutation', async () => {
 let release
 const called=[]
 const ipc=client(name=>{called.push(name);return name==='svc.SlowMutation'?new Promise(r=>{release=r}):Promise.resolve('ok')})
 const slow=ipc.wailsCallRaw('SlowMutation')
 await tick()
 assert.equal(await ipc.wailsCallRaw('GetSessionMessages'),'ok')
 assert.equal(await ipc.wailsCallRaw('OtherMutation'),'ok')
 assert.deepEqual(called,['svc.SlowMutation','svc.GetSessionMessages','svc.OtherMutation'])
 release('done'); await slow
})

test('same method mutations remain ordered and recover after rejection', async () => {
 let release
 let count=0
 const ipc=client(()=>++count===1?new Promise((_,reject)=>{release=()=>reject(Error('failure'))}):Promise.resolve('second'))
 const first=ipc.wailsCallRaw('UpdateState')
 const rejection=assert.rejects(first,/failure/)
 const second=ipc.wailsCallRaw('UpdateState')
 await tick(); assert.equal(count,1)
 release(); await rejection
 assert.equal(await second,'second'); assert.equal(count,2)
})

test('custom timeout replaces the default deadline', async () => {
 const timers=[]
 const ipc=client(()=>Promise.resolve('ok'),timers)
 assert.equal(await ipc.wailsCallWithTimeout('SlowMutation',60000),'ok')
 assert.deepEqual(timers,[60000])
})

test('approving or answering waits for the whole resumed run, not 15 seconds', async () => {
 const timers=[]
 const ipc=client(()=>Promise.resolve('ok'),timers)
 await ipc.wailsCallRaw('ResolveApproval','a',true,'','')
 await ipc.wailsCallRaw('AnswerQuestion','q','x')
 await ipc.wailsCallRaw('SomeOtherMutation')
 assert.deepEqual(timers,[960000,960000,15000])
})

test('timeout forwards cancellation to the actual Wails promise', async () => {
 let canceled=0
 const pending=new Promise(()=>{})
 pending.cancel=()=>{canceled++}
 const ipc=client(()=>pending)
 await assert.rejects(ipc.wailsCallUrgent('CancelTest',20),/已请求取消/)
 assert.equal(canceled,1)
})

test('late runtime availability does not dispatch an expired mutation', async () => {
 let release,called=0
 const mod={exports:{}}
 const req=id=>id==='./runtime'?{SVC:'svc',loadRuntime:()=>new Promise(r=>{release=r})}:{mockCall:()=>{}}
 new Function('require','module','exports','setTimeout','clearTimeout',compiled)(req,mod,mod.exports,setTimeout,clearTimeout)
 await assert.rejects(mod.exports.wailsCallUrgent('Expired',10),/超时/)
 release({Call:{ByName:()=>{called++;return Promise.resolve()}}})
 await tick();assert.equal(called,0)
})
