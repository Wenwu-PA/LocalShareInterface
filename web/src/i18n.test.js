import test from 'node:test';
import assert from 'node:assert/strict';
import {t,createTranslator,supportedLocales} from './i18n/index.js';
import ru from './i18n/ru.js';
test('Russian catalog covers navigation, card and audit labels',()=>{
 assert.deepEqual(supportedLocales,['ru']);
 for(const id of ['overview','devices','incidents','files','notifications','external','settings'])assert.notEqual(t('nav.'+id),'nav.'+id);
 assert.equal(t('audit.share.create'),'Создание ссылки');
 assert.equal(t('card.devices'),'Устройства');
 assert.equal(t('status.down'),'Сбой');
 assert.equal(t('missing.key'),'missing.key');
 assert.equal(t('nav.files',{},'unknown'),t('nav.files'));
 assert.equal(Object.keys(ru).length, new Set(Object.keys(ru)).size);
});
test('new locales can inherit untranslated messages and interpolate plain text',()=>{
 const translate=createTranslator({ru:{greeting:'Привет, {name}',fallback:'Резерв'},en:{greeting:'Hello, {name}'}},'ru');
 assert.equal(translate('greeting',{name:'Alex'},'en'),'Hello, Alex');
 assert.equal(translate('fallback',{},'en'),'Резерв');
 assert.equal(translate('greeting',{},'en'),'Hello, {name}');
 assert.equal(translate('greeting',{name:'<script>{name}</script>'},'en'),'Hello, <script>{name}</script>');
 assert.equal(translate('toString'),'toString');
 assert.equal(translate('greeting',Object.create({name:'inherited'}),'en'),'Hello, {name}');
 assert.equal(translate('greeting',{name:'Alex'},'__proto__'),'Привет, Alex');
});
