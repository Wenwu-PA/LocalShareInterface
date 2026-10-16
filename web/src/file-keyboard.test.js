import test from 'node:test';
import assert from 'node:assert/strict';
import {fileShortcut} from './file-keyboard.js';
test('physical shortcuts work with Russian layout and either command modifier',()=>{
 assert.equal(fileShortcut({code:'KeyA',key:'ф',ctrlKey:true}),'select');
 assert.equal(fileShortcut({code:'KeyF',key:'а',metaKey:true}),'search');
});
test('editing, modal, IME and repeated events are left alone',()=>{
 for(const state of [{editable:true},{blocked:true},{loading:true}])assert.equal(fileShortcut({code:'KeyA',ctrlKey:true},state),null);
 for(const flag of ['isComposing','repeat','defaultPrevented','altKey','shiftKey'])assert.equal(fileShortcut({code:'KeyA',ctrlKey:true,[flag]:true}),null);
});
test('destructive shortcuts require selection and never handle Shift Delete',()=>{
 assert.equal(fileShortcut({key:'Delete'}),null);
 assert.equal(fileShortcut({key:'Delete'},{selected:2}),'trash');
 assert.equal(fileShortcut({key:'Delete',shiftKey:true},{selected:2}),null);
 assert.equal(fileShortcut({key:'Delete'},{selected:2,loading:true}),null);
 assert.equal(fileShortcut({key:'F2'},{selected:1}),'rename');
 assert.equal(fileShortcut({key:'F2'},{selected:2}),null);
 assert.equal(fileShortcut({key:'Escape'},{selected:2}),'clear');
});
