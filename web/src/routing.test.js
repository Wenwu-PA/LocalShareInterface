import test from 'node:test';
import assert from 'node:assert/strict';
import {resolvePage,homePages} from './routing.js';

test('saved home applies only to an empty route',()=>{
 for(const home of homePages)assert.equal(resolvePage('',home),home);
 assert.equal(resolvePage('settings','files'),'settings');
 assert.equal(resolvePage('overview','files'),'overview');
 assert.equal(resolvePage('unknown','files'),'unknown');
});
test('old or invalid home preferences fall back to overview',()=>{
 for(const home of [undefined,null,'','https://example.com','external','admin'])assert.equal(resolvePage('',home),'overview');
});
