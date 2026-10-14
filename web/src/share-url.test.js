import test from 'node:test';
import assert from 'node:assert/strict';
import {shareURL,localOnly} from './share-url.js';
test('build HTTPS links including IPv6',()=>{assert.equal(shareURL('https://192.168.1.2:8766/','/s/token'),'https://192.168.1.2:8766/s/token');assert.equal(shareURL('https://[::1]:8766','/'),'https://[::1]:8766/')});
test('reject ambiguous or unsafe server origins',()=>{for(const v of ['http://host','javascript:alert(1)','https://user:password@host','https://host/path','https://host/?secret=1','https://host/#foo'])assert.throws(()=>shareURL(v,'/s/token'))});
test('identify loopback addresses',()=>{for(const v of ['https://localhost:8766','https://127.2.3.4','https://[::1]'])assert.equal(localOnly(v),true);assert.equal(localOnly('https://192.168.0.3'),false)});
