from pathlib import Path
import html, math, zipfile
P=Path(__file__).parent
dark=dict(bg='#0B0D11',panel='#12161D',raised='#1B2431',line='#283240',text='#EFF3FA',muted='#95A2B4',blue='#568FFF',green='#43C995',red='#FF7580',amber='#E6B660')
light=dict(dark,bg='#F2F5F9',panel='#FFFFFF',raised='#E6EDF8',line='#D7E0EC',text='#172131',muted='#57667B',blue='#2464DD',green='#16825B',red='#BF3146',amber='#936712')
class UI:
 def __init__(self,w=1440,h=1000,c=dark):
  self.w=w;self.h=h;self.c=c;self.s=[];self.rect(0,0,w,h,'bg',0)
 def rect(self,x,y,w,h,c='panel',r=8,stroke=None):
  self.s.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{r}" fill="{self.c.get(c,c)}"'+(f' stroke="{self.c.get(stroke,stroke)}"' if stroke else '')+'/>')
 def text(self,x,y,t,size=14,col='text',weight=400):
  self.s.append(f'<text x="{x}" y="{y}" font-family="Inter,DejaVu Sans,sans-serif" font-size="{size}" font-weight="{weight}" fill="{self.c.get(col,col)}">{html.escape(t)}</text>')
 def line(self,x,y,x2,y2,col='line'):
  self.s.append(f'<path d="M{x} {y} L{x2} {y2}" stroke="{self.c[col]}"/>')
 def button(self,x,y,t,w=176,primary=True):
  self.rect(x,y,w,40,'blue' if primary else 'raised',6);self.text(x+16,y+25,t,13,'#FFFFFF' if primary else 'text',600)
 def panel(self,x,y,w,h,title):
  self.rect(x,y,w,h,stroke='line');self.text(x+24,y+34,title,17,weight=600)
 def stat(self,x,y,w,label,value,note,col='text'):
  self.rect(x,y,w,132,stroke='line');self.text(x+20,y+27,label,11,'muted',600);self.text(x+20,y+72,value,30,col,600);self.text(x+20,y+105,note,12,'muted')
 def shell(self,title,sub,active,action):
  self.rect(0,0,232,1000,'panel',0);self.line(232,0,232,1000)
  self.rect(24,30,30,30,'blue',5);self.text(30,51,'L',21,'#FFFFFF',700);self.text(66,52,'LANBRIDGE',18,weight=700);self.text(25,84,'NETWORK OPERATIONS',10,'muted',600)
  self.rect(20,115,192,67,'bg',6,'line');self.text(34,141,'Домашняя сеть',13,weight=600);self.text(34,164,'●  lanbridge.local',11,'green')
  for i,n in enumerate(['Обзор сети','Устройства','Инциденты','Уведомления','Файлы','Внешний доступ']):
   y=214+i*51
   if n==active:self.rect(16,y,200,42,'raised',6);self.rect(16,y+10,3,22,'blue',1)
   self.text(32,y+26,f'{i+1:02}',11,'blue' if n==active else 'muted');self.text(64,y+26,n,13,'text' if n==active else 'muted',500)
  self.line(24,867,208,867);self.text(28,901,'Настройки',13,'muted');self.text(28,942,'Wenwu · Администратор',11);self.text(28,974,'v0.1 / Design preview',10,'muted')
  self.text(264,44,'Домашняя сеть  /  '+active,12,'muted');self.text(1035,44,'Демо-данные · 09.10.2026, 17:42',11,'muted');self.line(264,65,1408,65)
  self.text(264,116,title,30,weight=600);self.text(264,147,sub,13,'muted');self.button(1210,98,action,198)
 def table(self,y,title,headers,rows,widths):
  self.panel(264,y,1144,104+56*len(rows),title);xx=288
  for h,w in zip(headers,widths):self.text(xx,y+77,h,10,'muted',600);xx+=w
  for j,row in enumerate(rows):
   yy=y+99+j*56;self.line(288,yy,1384,yy);xx=288
   for v,w in zip(row,widths):
    col='red' if any(s in v for s in ['Сбой','Критический']) else 'green' if any(s in v for s in ['Работает','Восстановлен','Доставлено']) else 'text'
    self.text(xx,yy+34,v,13,col);xx+=w
 def save(self,name):
  s=f'<svg xmlns="http://www.w3.org/2000/svg" width="{self.w}" height="{self.h}" viewBox="0 0 {self.w} {self.h}"><title>LanBridge — {html.escape(name)}</title>'+''.join(self.s)+'</svg>'
  (P/(name+'.svg')).write_text(s);return s
def overview(c,name):
 u=UI(c=c);u.shell('Обзор сети','Сеть, сервер и сервисы — состояние в реальном времени.','Обзор сети','Добавить проверку')
 for i,data in enumerate([('ДОСТУПНОСТЬ / 30 ДНЕЙ','99,98%','1 инцидент · 8 мин простоя'),('ИНТЕРНЕТ','18 мс','Соединение восстановлено'),('УСТРОЙСТВА','12 / 13','1 устройство недоступно'),('СЕРВЕР','42% CPU','RAM 6,2 / 16 ГБ · Диск 64%')]):u.stat(264+290*i,186,274,*data)
 u.panel(264,342,722,294,'Задержка соединения');u.text(288,405,'мс',11,'muted');u.text(651,377,'● WAN',11,'blue');u.text(737,377,'● LAN',11,'green');u.text(823,377,'1 час ▾',11,'muted')
 for i,n in enumerate([60,40,20,0]):
  yy=423+i*49;u.text(288,yy+4,str(n),10,'muted');u.line(321,yy,960,yy)
 for series,col in [(0,'blue'),(1,'green')]:
  points=[]
  for i in range(65):
   val=18+3*math.sin(i*1.6)+2*math.cos(i*.9) if not series else 2+.6*math.sin(i)
   if i==33 and not series:val=53
   points.append(f'{321+i*9.96},{570-val*2.45}')
  u.s.append(f'<polyline points="{" ".join(points)}" fill="none" stroke="{c[col]}" stroke-width="2.2"/>')
 for i,s in enumerate(['16:45','17:00','17:15','17:30','17:45']):u.text(321+i*149,604,s,10,'muted')
 u.panel(1006,342,402,294,'Состояние инфраструктуры')
 for i,(a,b,col) in enumerate([('Интернет · WAN','Работает','green'),('Шлюз · 192.168.1.1','Работает','green'),('DNS · имена','Работает','green'),('NAS · хранилище','Сбой','red'),('Внешний heartbeat','8 с назад','green')]):
  y=416+i*42;u.text(1030,y,a,12,'muted');u.text(1280,y,b,12,col)
 u.table(660,'Проверки и сервисы',['ОБЪЕКТ','АДРЕС / МЕТОД','СТАТУС','ОТКЛИК','ПРОВЕРЕНО'],[['AX6000 · шлюз','192.168.1.1 · ICMP','Работает','1 мс','3 с назад'],['BE7000 · точка доступа','192.168.1.2 · ICMP','Работает','2 мс','5 с назад'],['NAS · хранилище','192.168.1.20 · TCP 445','Сбой','Тайм-аут','8 с назад'],['Minecraft','localhost:25565 · TCP','Работает','4 мс','6 с назад']],[260,320,190,150,150]);u.save(name)
overview(dark,'01-overview-dark');overview(light,'07-overview-light')
u=UI();u.shell('Инциденты','История сбоев, причины и доставка уведомлений.','Инциденты','Экспорт журнала');u.rect(264,187,1144,102,'panel',8,'red');u.text(288,224,'●  NAS недоступен · требуется внимание',18,'red',600);u.text(288,258,'INC-024 · С 17:36:12 · 3 ошибки TCP подряд · Telegram: доставлено в 17:36:44',13,'muted')
u.table(313,'Журнал событий',['СОБЫТИЕ','НАЧАЛО','ДЛИТЕЛЬНОСТЬ','СОСТОЯНИЕ'],[['NAS · TCP 445 недоступен','Сегодня, 17:36','6 мин','Критический'],['WAN · потеря интернета','Сегодня, 14:08','8 мин 12 с','Восстановлен'],['DNS · превышен тайм-аут','Вчера, 21:14','42 с','Восстановлен']],[440,220,220,216]);u.panel(264,610,1144,277,'INC-023 / Потеря интернета')
for i,s in enumerate(['14:08:00  Не отвечают 3 внешние цели. Локальный шлюз доступен.','14:08:30  Сбой подтверждён. Событие записано в локальный журнал.','14:09:00  Внешний наблюдатель: heartbeat пропущен. Telegram доставлен.','14:16:12  Соединение восстановлено. Отправлено уведомление.']):u.text(306,692+i*45,s,14,'muted');u.rect(286,682+i*45,6,6,'blue',3)
u.save('02-incidents')
u=UI();u.shell('Уведомления','Каналы доставки и правила эскалации для вашей сети.','Уведомления','Сохранить');u.panel(264,186,562,321,'Каналы доставки');u.panel(846,186,562,321,'Когда уведомлять')
for i,(a,b) in enumerate([('Telegram','Подключён · чат администратора'),('Email / SMTP','Подключён · alerts@example.com'),('Webhook','Не настроен')]):u.text(288,270+i*65,a,14,weight=600);u.text(288,294+i*65,b,12,'muted')
u.button(624,447,'Отправить тест',178,False)
for i,s in enumerate(['✓  Потеря интернета или недоступность сервиса','✓  Восстановление после сбоя','Порог: 3 ошибки подряд · интервал 10 секунд','Повтор: через 15 минут, пока сбой продолжается','Тихие часы: выключены','Критические события: отправлять всегда']):u.text(870,272+i*37,s,12,'muted')
u.panel(264,531,1144,235,'Внешний наблюдатель');u.text(288,609,'● Подключён · последний heartbeat 8 секунд назад',14,'green');u.text(288,648,'Сервер отправляет heartbeat каждые 30 секунд. Нет сигнала 90 секунд — наблюдатель отправляет тревогу.',13,'muted');u.text(288,675,'Уведомления работают даже при потере домашнего интернета или отключении самого сервера.',13,'muted');u.button(288,700,'Проверить наблюдатель',223,False);u.button(527,700,'Журнал доставки',190,False)
u.panel(264,790,1144,130,'Если внешнего наблюдателя нет');u.text(288,860,'При полном обрыве WAN сообщения ждут восстановления. Для немедленной доставки нужен резервный канал.',13,'amber');u.save('03-notifications')
u=UI();u.shell('Файлы','Общая папка сервера · доступ по учётным записям.','Файлы','Загрузить файлы')
for i,d in enumerate([('ХРАНИЛИЩЕ','640 / 1000 ГБ','360 ГБ свободно'),('ОБЩИЕ ФАЙЛЫ','1 248','Обновлено сегодня в 17:30'),('ВРЕМЕННЫЕ ССЫЛКИ','3 активны','Ближайшая истекает через 2 ч')]):u.stat(264+388*i,186,368,*d)
u.text(264,365,'Общая папка / Документы',14);u.button(984,340,'Новая папка',146,False);u.button(1146,340,'Подключить телефон',262,False)
u.table(404,'Содержимое папки',['ИМЯ','РАЗМЕР','ИЗМЕНЁН','ДОСТУП'],[['Резервные копии /','12 файлов','Сегодня, 03:00','Администратор'],['Медиа /','86 файлов','Вчера, 18:20','Пользователи'],['network-config.yaml','4 КБ','Сегодня, 16:45','Администратор'],['server-backup.tar.gz','2,4 ГБ','Сегодня, 03:00','Администратор']],[430,170,270,220]);u.panel(264,756,1144,170,'Временная ссылка · server-backup.tar.gz');u.text(288,831,'Срок: 24 часа · Пароль: включён · Лимит: 1 скачивание · Доступ: только скачивание',13,'muted');u.button(288,854,'Создать ссылку',176,False);u.save('04-files')
u=UI();u.shell('Внешний доступ','Подключение извне и контроль безопасности.','Внешний доступ','Проверить доступ')
for i,d in enumerate([('ПУБЛИЧНЫЙ ДОСТУП','Выключен','Доступ только из локальной сети'),('АДРЕС ПРОВАЙДЕРА','CGNAT','Прямой входящий доступ недоступен'),('ЗАЩИТА','HTTPS','Сертификат действителен')]):u.stat(264+388*i,186,368,*d)
u.panel(264,342,562,364,'Способ подключения');u.panel(846,342,562,364,'Проверка перед включением')
for i,(a,b) in enumerate([('●  NetBird · приватная сеть','Только устройства, добавленные в вашу сеть.'),('○  Cloudflare Tunnel · публичный HTTPS','Домен и отдельная политика доступа.'),('○  SSH-туннель · собственный VPS','Для сервера с собственным внешним адресом.')]):u.text(288,422+i*75,a,14,'blue' if i==0 else 'text');u.text(288,449+i*75,b,12,'muted')
u.button(288,642,'Настроить NetBird',203)
for i,s in enumerate(['✓  HTTPS настроен','✓  Администратор создан','✓  Анонимный доступ к файлам выключен','✓  Журнал входов включён','—  Провайдер туннеля не подключён']):u.text(870,425+i*44,s,13,'muted')
u.panel(264,730,1144,155,'Последняя проверка');u.text(288,808,'Локально: https://lanbridge.local · доступен',14,'green');u.text(288,844,'Из интернета: недоступен · туннель выключен',14,'muted');u.save('05-external-access')
for mode,c in [('dark',dark),('light',light)]:
 u=UI(390,1000,c);u.text(20,42,'LANBRIDGE',17,weight=700);u.text(326,42,'17:42',12,'muted');u.line(20,64,370,64);u.text(20,108,'Обзор сети',28,weight=600);u.text(20,138,'Домашняя сеть · демо-данные',12,'muted');u.rect(20,163,350,107,'panel',8,'red');u.text(36,202,'●  NAS недоступен',18,'red',600);u.text(36,235,'Сбой 6 минут · уведомление доставлено',11,'muted');u.stat(20,290,169,'ИНТЕРНЕТ','18 мс','Работает');u.stat(201,290,169,'УСТРОЙСТВА','12 / 13','1 недоступно');u.panel(20,442,350,265,'Проверки')
 for i,(a,b) in enumerate([('AX6000 · шлюз','1 мс'),('BE7000 · Wi-Fi','2 мс'),('NAS · TCP 445','Сбой'),('Minecraft','4 мс'),('Heartbeat','8 с назад')]):u.text(44,522+i*37,a,13,'muted');u.text(276,522+i*37,b,12,'red' if b=='Сбой' else 'green')
 u.panel(20,727,350,137,'Последний инцидент');u.text(44,800,'Интернет восстановлен',14,'green');u.text(44,834,'14:16 · простой 8 мин 12 с',12,'muted');u.button(20,884,'Открыть журнал',350,False);u.text(24,967,'Обзор     Сбои     Файлы     Ещё',14,'muted');u.save('06-mobile-'+mode)
print('Created',len(list(P.glob('*.svg'))),'SVG screens')
