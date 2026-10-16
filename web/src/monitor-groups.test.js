import test from 'node:test';
import assert from 'node:assert/strict';
import {monitorGroups} from './monitor-groups.js';
const monitor=(group,status='up',paused=false,latency=10)=>({group,paused,state:{status,latency,checked:1}});
test('configured order then custom groups, case insensitive membership',()=>{
 const result=monitorGroups([monitor('lan'),monitor('VPN'),monitor(' WAN ')],['LAN','WAN']);
 assert.deepEqual(result.map(g=>g.name),['LAN','WAN','VPN']);
 assert.deepEqual(result.map(g=>g.members.length),[1,1,1]);
});
test('group health covers all active monitors and ignores paused failures',()=>{
 assert.equal(monitorGroups([monitor('WAN'),monitor('wan','down')],['WAN'])[0].status,'down');
 assert.equal(monitorGroups([monitor('WAN'),monitor('wan','down',true)],['WAN'])[0].status,'up');
 assert.equal(monitorGroups([monitor('WAN','down',true)],['WAN'])[0].status,'paused');
 assert.equal(monitorGroups([monitor('WAN','pending')],['WAN'])[0].status,'pending');
 assert.equal(monitorGroups([],['WAN'])[0].status,'empty');
});
test('latency averages successful active responses only',()=>{
 assert.equal(monitorGroups([monitor('WAN','up',false,10),monitor('WAN','up',false,30),monitor('WAN','down',false,0)],['WAN'])[0].latency,20);
 assert.equal(monitorGroups([monitor('WAN','down')],['WAN'])[0].latency,null);
});
