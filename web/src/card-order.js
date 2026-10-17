export const defaultCardOrder=['availability','internet','checks','devices'];
export const cardLabels={availability:'Доступность',internet:'Интернет',checks:'Проверки и сервисы',devices:'Устройства'};
export function normalizeCardOrder(order){
 const valid=Array.isArray(order)?[...new Set(order.filter(id=>defaultCardOrder.includes(id)))]:[];
 return [...valid,...defaultCardOrder.filter(id=>!valid.includes(id))];
}
export function moveCard(order,id,step){
 const result=normalizeCardOrder(order),from=result.indexOf(id),to=from+step;
 if(from<0||![-1,1].includes(step)||to<0||to>=result.length)return result;
 [result[from],result[to]]=[result[to],result[from]];return result;
}
