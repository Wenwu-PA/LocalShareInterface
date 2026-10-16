import {h,createContext} from 'preact';
import {useContext} from 'preact/hooks';
import {formatDate} from './date-format.js';
export const TimePreferences=createContext({});
function DateDisplay({value,mode}) {
 const prefs=useContext(TimePreferences),text=formatDate(value,prefs,mode);
 return h('time',{dateTime:text==='—'?undefined:new Date(value).toISOString(),title:prefs.timezone||'Часовой пояс браузера'},text);
}
export const dateStamp=(value,mode='datetime')=>h(DateDisplay,{value,mode});
