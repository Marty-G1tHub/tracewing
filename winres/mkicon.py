from PIL import Image, ImageDraw
import math
S=1024
im=Image.new("RGBA",(S,S),(0,0,0,0))
# gradient rounded square
g=Image.new("RGBA",(S,S))
px=g.load()
c1=(79,124,255); c2=(124,92,255)
for y in range(S):
    for x in range(S):
        t=(x+y)/(2*S-2)
        px[x,y]=tuple(int(c1[i]+(c2[i]-c1[i])*t) for i in range(3))+(255,)
m=Image.new("L",(S,S),0)
ImageDraw.Draw(m).rounded_rectangle((40,40,S-40,S-40),radius=230,fill=255)
im.paste(g,(0,0),m)
lay=Image.new("RGBA",(S,S),(0,0,0,0)); d=ImageDraw.Draw(lay)
cx,cy,R=S//2,int(S*0.57),300
w=70
# track arc 200..340 deg (PIL angles clockwise from 3 o'clock): gauge from 150 to 390
tr=Image.new("RGBA",(S,S),(0,0,0,0)); td=ImageDraw.Draw(tr)
td.arc((cx-R,cy-R,cx+R,cy+R),150,390,fill=(255,255,255,80),width=w)
d.arc((cx-R,cy-R,cx+R,cy+R),150,320,fill=(255,255,255,255),width=w)
# round caps
def cap(a):
    r=R-w/2
    x=cx+r*math.cos(math.radians(a)); y=cy+r*math.sin(math.radians(a))
    d.ellipse((x-w/2,y-w/2,x+w/2,y+w/2),fill=(255,255,255,255))
cap(150);cap(320)
x=cx+(R-w/2)*math.cos(math.radians(390)); y=cy+(R-w/2)*math.sin(math.radians(390))
td.ellipse((x-w/2,y-w/2,x+w/2,y+w/2),fill=(255,255,255,80))
im=Image.alpha_composite(im,tr)
# needle
a=math.radians(320)
nx=cx+ (R-170)*math.cos(a); ny=cy+(R-170)*math.sin(a)
d.line((cx,cy,nx,ny),fill=(255,255,255,255),width=58)
d.ellipse((nx-29,ny-29,nx+29,ny+29),fill=(255,255,255,255))
d.ellipse((cx-60,cy-60,cx+60,cy+60),fill=(255,255,255,255))
d.ellipse((cx-24,cy-24,cx+24,cy+24),fill=(100,108,255,255))
im=Image.alpha_composite(im,lay)
im.save("icon-1024.png")
im.resize((256,256),Image.LANCZOS).save("icon.ico",sizes=[(16,16),(24,24),(32,32),(48,48),(64,64),(128,128),(256,256)])
