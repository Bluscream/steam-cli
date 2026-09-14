package webapi

import (
 "context"
 _ "embed"
 "encoding/json"
 "errors"
 "sort"
)
//go:embed data/xpaw.json
var xpawJSON []byte

// ExtendedCatalog preserves nested parameter metadata from xPaw's MIT-licensed
// catalog. Unknown verbs remain unknown instead of silently assuming GET.
func ExtendedCatalog()(Catalog,error){return ParseExtended(xpawJSON)}
func ParseExtended(b []byte)(Catalog,error){
 var raw map[string]map[string]Method
 if e:=json.Unmarshal(b,&raw);e!=nil||len(raw)==0{return Catalog{},errors.New("invalid extended API catalog")}
 var c Catalog
 for iface,methods:=range raw{if !identifier.MatchString(iface){return Catalog{},errors.New("invalid extended interface")};i:=Interface{Name:iface}
  for name,m:=range methods{if !identifier.MatchString(name)||m.Version<1{return Catalog{},errors.New("invalid extended method")};m.Name=name;i.Methods=append(i.Methods,m)}
  sort.Slice(i.Methods,func(a,b int)bool{return i.Methods[a].Name<i.Methods[b].Name});c.APIList.Interfaces=append(c.APIList.Interfaces,i)
 };sort.Slice(c.APIList.Interfaces,func(a,b int)bool{return c.APIList.Interfaces[a].Name<c.APIList.Interfaces[b].Name});return c,nil
}
func(c *Client)Discover(ctx context.Context,source string,refresh bool)(Catalog,error){
 if source=="live"{return c.Catalog(ctx,refresh)}
 x,e:=ExtendedCatalog();if e!=nil{return x,e};if source=="xpaw"{return x,nil}
 if source!="all"{return Catalog{},errors.New("catalog must be all, live, or xpaw")}
 live,e:=c.Catalog(ctx,refresh);if e!=nil{
  // The bundled complete reference remains usable offline. Online failures
  // are surfaced so bad credentials are never hidden by a fallback.
  if c.HTTP.Offline{return x,nil};return Catalog{},e
 }
 return MergeCatalogs(x,live),nil
}
func MergeCatalogs(a,b Catalog)Catalog{
 type key struct{iface,name string;version int};m:=map[key]Method{}
 for _,c:=range []Catalog{a,b}{for _,i:=range c.APIList.Interfaces{for _,v:=range i.Methods{k:=key{i.Name,v.Name,v.Version};if old,ok:=m[k];ok{if v.Source==""{v.Source=old.Source};if v.Description==""{v.Description=old.Description};if len(v.Parameters)==0{v.Parameters=old.Parameters}};m[k]=v}}}
 groups:=map[string][]Method{};for k,v:=range m{groups[k.iface]=append(groups[k.iface],v)}
 var out Catalog;for name,methods:=range groups{sort.Slice(methods,func(i,j int)bool{if methods[i].Name==methods[j].Name{return methods[i].Version<methods[j].Version};return methods[i].Name<methods[j].Name});out.APIList.Interfaces=append(out.APIList.Interfaces,Interface{Name:name,Methods:methods})};sort.Slice(out.APIList.Interfaces,func(i,j int)bool{return out.APIList.Interfaces[i].Name<out.APIList.Interfaces[j].Name});return out
}
