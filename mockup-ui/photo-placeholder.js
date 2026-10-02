// ThumbHash decoding adapted from go.n16f.net/thumbhash (ISC).
// Copyright (c) 2023 Nicolas Martyanoff <nicolas@n16f.net>
// Full notice: /thumbhash-LICENSE.txt. Private pixels live only in mounted DOM.
export function placeholderPixels(encoded) {
  const data=Uint8Array.from(atob(encoded),c=>c.charCodeAt(0));
  if(data.length<5 || data.length>128)throw new Error('Invalid placeholder');
  const h24=data[0]|data[1]<<8|data[2]<<16,h16=data[3]|data[4]<<8,alpha=!!(h24>>23),landscape=!!(h16>>15);
  const lx=landscape?(alpha?5:7):Math.max(3,h16&7),ly=landscape?Math.max(3,h16&7):(alpha?5:7);
  const start=alpha?6:5;let index=0;
  const channel=(nx,ny,scale,dc)=>{
    const coefficients=[];
    for(let cy=0;cy<ny;cy++)for(let cx=cy?0:1;cx*ny<nx*(ny-cy);cx++){
      const offset=start+(index>>1);if(offset>=data.length)throw new Error('Truncated placeholder');
      coefficients.push([cx,cy,(((data[offset]>>((index&1)*4))&15)/7.5-1)*scale]);index++;
    }
    return {dc,coefficients};
  };
  const channels=[channel(lx,ly,((h24>>18)&31)/31,(h24&63)/63),channel(3,3,((h16>>3)&63)/63*1.25,((h24>>6)&63)/31.5-1),channel(3,3,((h16>>9)&63)/63*1.25,((h24>>12)&63)/31.5-1)];
  if(alpha)channels.push(channel(5,5,(data[5]>>4)/15,(data[5]&15)/15));
  const width=lx>ly?32:Math.round(32*lx/ly),height=lx>ly?Math.round(32*ly/lx):32;
  const pixels=new Uint8ClampedArray(width*height*4);
  for(let y=0;y<height;y++)for(let x=0;x<width;x++){
    const fx=Array.from({length:7},(_,i)=>Math.cos(Math.PI*(x+.5)*i/width));
    const fy=Array.from({length:7},(_,i)=>Math.cos(Math.PI*(y+.5)*i/height));
    const [l,p,q,a=1]=channels.map(c=>c.coefficients.reduce((sum,[cx,cy,k])=>sum+k*fx[cx]*fy[cy]*2,c.dc));
    const b=l-2*p/3,r=(3*l-b+q)/2,g=r-q;
    pixels.set([r*255,g*255,b*255,a*255],(y*width+x)*4);
  }
  return {width,height,pixels};
}
export function photoPlaceholder(hash){
  try{
    const {width,height,pixels}=placeholderPixels(hash),canvas=document.createElement('canvas');canvas.width=width;canvas.height=height;
    canvas.getContext('2d').putImageData(new ImageData(pixels,width,height),0,0);
    return `url(${canvas.toDataURL('image/png')})`;
  }catch{return 'none';}
}
