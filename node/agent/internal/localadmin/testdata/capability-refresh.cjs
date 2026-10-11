const fs=require('fs'),vm=require('vm'),assert=require('assert');
const script=fs.readFileSync(process.argv[2],'utf8');
const fn=script.slice(script.indexOf('async function refresh(passive)'),script.indexOf('function tick()'));
const state={auth:{authenticated:true},refreshing:false,status:null,caps:null,node:null,wifi:null,radios:null,poll:null,POLL_MS:1,T:{},calls:0,current:false,fail:false};
Object.assign(state,{setState(){},render(){},$:()=>({}),t:()=>'',when:()=>null,clearTimeout(){},setTimeout:()=>1,tick(){},api:async(path)=>{if(path==='capabilities'){state.calls++;if(state.fail)throw Error('read failed');return {sms:state.current};}return {};}});
vm.createContext(state);vm.runInContext(fn,state);
(async()=>{await state.refresh(true);assert.equal(state.caps.sms,false);state.current=true;await state.refresh(true);assert.equal(state.caps.sms,true);assert.equal(state.calls,2);state.fail=true;await state.refresh(true);assert.equal(state.caps,null);state.fail=false;await state.refresh(true);assert.equal(state.caps.sms,true);console.log('Capability recovery and read failure PASS');})().catch(e=>{console.error(e);process.exit(1);});
