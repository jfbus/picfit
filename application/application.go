package application

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/getsentry/raven-go"
	"github.com/gorilla/mux"
	"github.com/thoas/gokvstores"
	"github.com/thoas/gostorages"
	"github.com/thoas/picfit/hash"
	"github.com/thoas/picfit/image"
	"github.com/thoas/picfit/signature"
	"net/url"
	"strings"
)

type Shard struct {
	Depth int
	Width int
}

type Application struct {
	Prefix        string
	SecretKey     string
	Format        string
	KVStore       gokvstores.KVStore
	SourceStorage gostorages.Storage
	DestStorage   gostorages.Storage
	Router        *mux.Router
	Shard         Shard
	Raven         *raven.Client
	Logger        *logrus.Logger
}

func NewApplication() *Application {
	return &Application{
		Logger: logrus.New(),
	}
}

func (a *Application) ShardFilename(filename string) string {
	results := hash.Shard(filename, a.Shard.Width, a.Shard.Depth, true)

	return strings.Join(results, "/")
}

func (a *Application) Store(i *image.ImageFile) error {
	con := App.KVStore.Connection()
	defer con.Close()

	err := i.Save()

	if err != nil {
		a.Logger.Fatal(err)
		return err
	}

	a.Logger.Infof("Save thumbnail %s to storage", i.Filepath)

	key := a.WithPrefix(i.Key)

	err = con.Set(key, i.Filepath)

	if err != nil {
		a.Logger.Fatal(err)

		return err
	}

	a.Logger.Infof("Save key %s => %s to kvstore", key, i.Filepath)

	return nil
}

func (a *Application) WithPrefix(str string) string {
	return a.Prefix + str
}

func (a *Application) ImageFileFromRequest(req *Request, async bool, load bool) (*image.ImageFile, error) {
	var file *image.ImageFile = &image.ImageFile{
		Key:     req.Key,
		Storage: a.DestStorage,
	}
	var err error

	key := a.WithPrefix(req.Key)

	// Image from the KVStore found
	stored, err := gokvstores.String(req.Connection.Get(key))

	file.Filepath = stored

	if stored != "" {
		a.Logger.Infof("Key %s found in kvstore: %s", key, stored)

		if load {
			file, err = image.FromStorage(a.DestStorage, stored)

			if err != nil {
				return nil, err
			}
		}
	} else {
		a.Logger.Infof("Key %s not found in kvstore", key)

		// Image not found from the KVStore, we need to process it
		// URL available in Query String
		if req.URL != nil {
			file, err = image.FromURL(req.URL)
		} else {
			// URL provided we use http protocol to retrieve it
			file, err = image.FromStorage(a.SourceStorage, req.Filepath)
		}

		if err != nil {
			return nil, err
		}

		file, err = file.Transform(req.Operation, req.QueryString)

		if err != nil {
			return nil, err
		}

		format := req.Format

		if format == "" {
			format = a.Format
		}

		file.Filepath = fmt.Sprintf("%s.%s", a.ShardFilename(req.Key), format)
		file.Storage = a.DestStorage
	}

	file.Key = req.Key

	if stored == "" {
		if async == true {
			go a.Store(file)
		} else {
			err = a.Store(file)
		}
	}

	return file, err
}
func (a *Application) IsValidSign(qs map[string]string) bool {
	if a.SecretKey == "" {
		return true
	}

	params := url.Values{}
	for k, v := range qs {
		params.Set(k, v)
	}

	return signature.VerifySign(a.SecretKey, params.Encode())
}
