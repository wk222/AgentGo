import { ref } from 'vue'
import { wailsCall } from '../shared/wails'
const KEY='agentgo.pinned-pages.v1'
const read=():string[]=>{try{const v=JSON.parse(localStorage.getItem(KEY)||'[]');return Array.isArray(v)?Array.from(new Set(v.filter((x:unknown)=>typeof x==='string'&&x.length>0))).slice(0,12):[]}catch{return []}}
export const pinnedPages=ref<string[]>(read())
export const generatedApps=ref<Record<string,any>[]>([])
export const generatedPageName=(id:string)=>id.startsWith('app:')?id.slice(4):''
export function togglePinnedPage(name:string) {
 pinnedPages.value=pinnedPages.value.includes(name)?pinnedPages.value.filter(x=>x!==name):[...pinnedPages.value,name].slice(-12)
 try{localStorage.setItem(KEY,JSON.stringify(pinnedPages.value))}catch{}
}
export async function loadGeneratedPages(){
 try{generatedApps.value=(await wailsCall('ListInnerApps',1000)).filter((x:any)=>x.kind==='ui'&&x.enabled!==false)}catch{}
}
