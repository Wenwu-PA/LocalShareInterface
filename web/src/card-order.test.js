import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultCardOrder,normalizeCardOrder,moveCard} from './card-order.js';
test('card order defaults and repairs old or malformed settings',()=>{
 assert.deepEqual(normalizeCardOrder(null),defaultCardOrder);
 assert.deepEqual(normalizeCardOrder(['devices','unknown','devices']),['devices','availability','internet','checks']);
});
test('move cards without mutating preferences or losing cards',()=>{
 const input=[...defaultCardOrder];
 assert.deepEqual(moveCard(input,'internet',-1),['internet','availability','checks','devices']);
 assert.deepEqual(moveCard(input,'availability',1),['internet','availability','checks','devices']);
 assert.deepEqual(moveCard(input,'devices',1),input);
 assert.deepEqual(moveCard(input,'availability',-1),input);
 assert.deepEqual(moveCard(input,'unknown',1),input);
 assert.deepEqual(moveCard(input,'internet',2),input);
 assert.deepEqual(input,defaultCardOrder);
});
